package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// unitNameRe guards against anything weird being passed to systemctl/journalctl.
// Service control runs via `sudo -n systemctl`, which works because the homelab
// service user has passwordless sudo. exec.Command passes args without a shell,
// so this is belt-and-suspenders against malformed names.
var unitNameRe = regexp.MustCompile(`^[a-zA-Z0-9@._:\-]+\.service$`)

type serviceView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      string `json:"active"` // active | inactive | failed | activating ...
	Sub         string `json:"sub"`    // running | exited | dead ...
	Enabled     string `json:"enabled"`
}

// ServicesList — GET /api/services
func ServicesList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "systemctl", "list-units", "--type=service",
		"--all", "--no-legend", "--no-pager", "--plain").Output()
	if err != nil {
		http.Error(w, "systemctl failed", http.StatusInternalServerError)
		return
	}

	enabled := map[string]string{}
	if enOut, err := exec.CommandContext(ctx, "systemctl", "list-unit-files",
		"--type=service", "--no-legend", "--no-pager").Output(); err == nil {
		for _, line := range strings.Split(string(enOut), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				enabled[f[0]] = f[1]
			}
		}
	}

	var views []serviceView
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		views = append(views, serviceView{
			Name:        f[0],
			Active:      f[2],
			Sub:         f[3],
			Description: strings.Join(f[4:], " "),
			Enabled:     enabled[f[0]],
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	writeJSON(w, views)
}

// ServicesAction — POST /api/services/action {name, action}
func ServicesAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if !unitNameRe.MatchString(req.Name) {
		http.Error(w, "Invalid service name", http.StatusBadRequest)
		return
	}
	switch req.Action {
	case "start", "stop", "restart", "enable", "disable":
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sudo", "-n", "systemctl", req.Action, req.Name).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// HostLogs — WS /ws/host/logs?unit=<service>  (live journalctl; unit optional)
func HostLogs(w http.ResponseWriter, r *http.Request) {
	unit := r.URL.Query().Get("unit")
	if unit != "" && !unitNameRe.MatchString(unit) {
		http.Error(w, "Invalid unit", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	args := []string{"-n", "journalctl", "-f", "-n", "300", "--no-pager", "-o", "short-iso"}
	if unit != "" {
		args = append(args, "-u", unit)
	}
	cmd := exec.CommandContext(ctx, "sudo", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		if err := conn.WriteMessage(websocket.TextMessage, append(scanner.Bytes(), '\n')); err != nil {
			break
		}
	}
	cancel()
	cmd.Wait()
}
