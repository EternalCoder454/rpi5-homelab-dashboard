package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The Agent gateway is the ONLY path through which the assistant (or the user
// via the Assistant tab) can cause side effects on the Pi. It does NOT run
// arbitrary shell: it exposes a fixed allowlist of named actions, each bound to
// a vetted script we ship in scripts/agent/. The model can only ever reference
// an action ID; it can never inject a command. Parameters are validated against
// a per-action spec, runs are single-flight with a timeout, and every run is
// written to an audit log. All actions are user-confirmed in the UI before they
// run — the assistant proposes, the human approves.

// ParamSpec describes the single optional argument an action accepts.
type ParamSpec string

const (
	ParamNone    ParamSpec = ""        // no argument
	ParamPercent ParamSpec = "percent" // integer 0-100 (fan speed)
)

// AgentAction is one entry in the allowlist.
type AgentAction struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	Category    string    `json:"category"`           // maintenance | system | hardware | docker
	Destructive bool      `json:"destructive"`        // surface an extra warning before running
	Param       ParamSpec `json:"param"`              // argument the action takes, if any
	Fixed       string    `json:"fixed,omitempty"`    // a fixed argument baked in (e.g. "auto")
	script      string    // bare script filename in scripts/agent/
}

// agentActions is the complete, fixed allowlist. To add a capability you add a
// vetted script and an entry here — there is no other way in.
var agentActions = []AgentAction{
	{ID: "maintenance-check", Label: "Maintenance Check", Category: "maintenance",
		Description: "Take a fresh backup, then report temperature, throttling, disk, failed services, pending updates and containers.",
		script:      "maintenance-check.sh"},
	{ID: "backup", Label: "Backup Now", Category: "maintenance",
		Description: "Back up dashboard state and key system config to the SD backup partition (rotating, keeps the last 10).",
		script:      "backup.sh"},
	{ID: "list-backups", Label: "List Backups", Category: "maintenance",
		Description: "Show the backups stored on the SD and how much space is free.",
		script:      "list-backups.sh"},
	{ID: "update", Label: "Update System", Category: "system", Destructive: true,
		Description: "apt update + full-upgrade + autoremove. Changes installed packages; may require a reboot afterwards.",
		script:      "update.sh"},
	{ID: "reboot", Label: "Reboot Pi", Category: "system", Destructive: true,
		Description: "Reboot the Pi. The dashboard is unreachable for ~30-60 seconds.",
		script:      "reboot.sh"},
	{ID: "fan-set", Label: "Set Fan Speed", Category: "hardware", Param: ParamPercent,
		Description: "Pin the Argon fan to a fixed speed (0-100%). Auto-forced to 100% at >=75C for safety.",
		script:      "fan.sh"},
	{ID: "fan-auto", Label: "Fan: Automatic", Category: "hardware", Fixed: "auto",
		Description: "Restore the automatic temperature-based fan curve.",
		script:      "fan.sh"},
	{ID: "docker-prune", Label: "Prune Docker", Category: "docker", Destructive: true,
		Description: "Reclaim space from dangling images, stopped containers, unused networks and build cache. Named volumes are left untouched.",
		script:      "docker-prune.sh"},
}

func findAction(id string) (AgentAction, bool) {
	for _, a := range agentActions {
		if a.ID == id {
			return a, true
		}
	}
	return AgentAction{}, false
}

// agentScriptDir resolves scripts/agent relative to the working directory (the
// install dir, ~/homelab on the Pi) to an absolute path, once.
var (
	agentDirOnce sync.Once
	agentDir     string
	agentMu      sync.Mutex // single-flight: one action at a time
	agentLogPath = "agent-actions.log"
)

func scriptDir() string {
	agentDirOnce.Do(func() {
		wd, _ := os.Getwd()
		agentDir = filepath.Join(wd, "scripts", "agent")
	})
	return agentDir
}

// AgentActions — GET /api/agent/actions. Returns the allowlist for the UI.
func AgentActions(w http.ResponseWriter, r *http.Request) {
	cats := map[string]int{"maintenance": 0, "hardware": 1, "docker": 2, "system": 3}
	out := make([]AgentAction, len(agentActions))
	copy(out, agentActions)
	sort.SliceStable(out, func(i, j int) bool {
		if cats[out[i].Category] != cats[out[j].Category] {
			return cats[out[i].Category] < cats[out[j].Category]
		}
		return out[i].Label < out[j].Label
	})
	writeJSON(w, out)
}

// AgentLog — GET /api/agent/log. Returns the recent audit entries (newest last).
func AgentLog(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(agentLogPath)
	if err != nil {
		writeJSON(w, []string{})
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	writeJSON(w, lines)
}

// AgentRun — WS /ws/agent/run. The client (the Assistant tab, after the user
// confirms) sends one frame: {"action":"<id>","param":"<arg>"}. The server
// validates it against the allowlist, runs the bound script with the validated
// argument, streams output line-by-line, and ends with a done frame.
//
//	{"line":"..."}              one line of output
//	{"done":true,"exit":N}      completion with the script's exit code
//	{"error":"..."}             rejected or failed to start
func AgentRun(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var in struct {
		Action string `json:"action"`
		Param  string `json:"param"`
	}
	if err := conn.ReadJSON(&in); err != nil {
		return
	}

	action, ok := findAction(in.Action)
	if !ok {
		agentErr(conn, "unknown action")
		return
	}

	arg, err := validateParam(action, in.Param)
	if err != nil {
		agentErr(conn, err.Error())
		return
	}

	if !agentMu.TryLock() {
		agentErr(conn, "another action is already running — wait for it to finish")
		return
	}
	defer agentMu.Unlock()

	script := filepath.Join(scriptDir(), action.script)
	if _, err := os.Stat(script); err != nil {
		agentErr(conn, "action script is not installed on this host: "+action.script)
		return
	}

	argv := []string{script}
	if arg != "" {
		argv = append(argv, arg)
	}

	// reboot/update can take a while; give them headroom.
	timeout := 5 * time.Minute
	if action.ID == "update" {
		timeout = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/bin/env", append([]string{"bash"}, argv...)...)
	pr, pw, _ := os.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	send := func(line string) {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		_ = conn.WriteJSON(map[string]any{"line": line})
	}
	send(fmt.Sprintf("$ %s%s", action.ID, paramSuffix(arg)))

	if err := cmd.Start(); err != nil {
		pw.Close()
		agentErr(conn, "could not start: "+err.Error())
		return
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait(); pw.Close() }()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		send(sc.Text())
	}

	exit := 0
	if e := <-waitErr; e != nil {
		if ee, ok := e.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
			send("error: " + e.Error())
		}
	}
	logAction(action.ID, arg, exit)

	conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = conn.WriteJSON(map[string]any{"done": true, "exit": exit})
}

func validateParam(a AgentAction, raw string) (string, error) {
	if a.Fixed != "" {
		return a.Fixed, nil // ignore any client-supplied value; the arg is baked in
	}
	switch a.Param {
	case ParamNone:
		return "", nil
	case ParamPercent:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 0 || n > 100 {
			return "", fmt.Errorf("fan speed must be a whole number from 0 to 100")
		}
		return strconv.Itoa(n), nil
	default:
		return "", fmt.Errorf("unsupported parameter")
	}
}

func paramSuffix(arg string) string {
	if arg == "" {
		return ""
	}
	return " " + arg
}

func agentErr(conn *websocket.Conn, msg string) {
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = conn.WriteJSON(map[string]any{"error": msg})
}

func logAction(id, arg string, exit int) {
	f, err := os.OpenFile(agentLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s%s\texit=%d\n", time.Now().Format(time.RFC3339), id, paramSuffix(arg), exit)
}

// agentActionsPrompt renders the allowlist for the assistant's system prompt so
// it can propose actions (which the user then confirms). The model emits a
// directive the UI turns into a Run button; it never executes anything itself.
func agentActionsPrompt() string {
	var b strings.Builder
	b.WriteString("\n[ACTIONS] You can propose ONE of these vetted actions when the user asks you to perform maintenance or control the Pi. ")
	b.WriteString("To propose one, end your reply with a directive on its own line: [[action:ID]] (for the fan speed use [[action:fan-set|NN]] with NN 0-100). ")
	b.WriteString("Only propose actions from this exact list. Never claim to have run anything — the user must click to confirm. Available:\n")
	for _, a := range agentActions {
		arg := ""
		if a.Param == ParamPercent {
			arg = " (takes a 0-100 percent)"
		}
		fmt.Fprintf(&b, "- %s: %s%s\n", a.ID, a.Description, arg)
	}
	return b.String()
}
