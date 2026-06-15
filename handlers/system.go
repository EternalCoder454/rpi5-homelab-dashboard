package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// primaryIP returns the host's primary outbound IPv4 by inspecting the route to
// a public address. No packets are actually sent — a UDP "connection" only
// resolves the source address the kernel would use.
func primaryIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return ""
}

// osLabel builds a friendly OS name, e.g. "Raspberry Pi OS Lite (64-bit)".
func osLabel() string {
	bits := "64-bit"
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		bits = "32-bit"
	}
	if d, err := os.ReadFile("/etc/rpi-issue"); err == nil {
		s := string(d)
		variant := ""
		switch {
		case strings.Contains(s, "stage2"):
			variant = "Lite "
		case strings.Contains(s, "stage4"):
			variant = "Desktop "
		case strings.Contains(s, "stage5"):
			variant = "Full "
		}
		return fmt.Sprintf("Raspberry Pi OS %s(%s)", variant, bits)
	}
	if d, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(d), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"") + " (" + bits + ")"
			}
		}
	}
	return "Linux (" + bits + ")"
}

// --- system info ------------------------------------------------------------

// SystemInfo — GET /api/system/info
func SystemInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{}

	if d, err := os.ReadFile("/proc/uptime"); err == nil {
		var up float64
		fmt.Sscanf(string(d), "%f", &up)
		info["uptime_seconds"] = int(up)
	}
	if d, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		info["kernel"] = strings.TrimSpace(string(d))
	}
	if d, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		info["model"] = strings.TrimRight(string(d), "\x00")
	}
	info["os"] = osLabel()
	if h, err := os.Hostname(); err == nil {
		info["hostname"] = h
	}
	info["arch"] = runtime.GOARCH
	info["cores"] = runtime.NumCPU()
	if ip := primaryIP(); ip != "" {
		info["ip"] = ip
	}
	if out, err := exec.Command("vcgencmd", "get_throttled").Output(); err == nil {
		info["throttled"] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "throttled="))
	}
	_, rebootErr := os.Stat("/var/run/reboot-required")
	info["reboot_required"] = rebootErr == nil

	count, sec := upgradableCounts()
	info["updates"] = map[string]int{"count": count, "security": sec}

	writeJSON(w, info)
}

func upgradableCounts() (count, sec int) {
	out, err := exec.Command("apt", "list", "--upgradable").Output() // reads cached lists, no root
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || strings.HasPrefix(line, "Listing") {
			continue
		}
		count++
		if strings.Contains(line, "security") {
			sec++
		}
	}
	return
}

// --- power ------------------------------------------------------------------

// SystemPower — POST /api/system/power {action: reboot|poweroff}
func SystemPower(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	var verb string
	switch req.Action {
	case "reboot":
		verb = "reboot"
	case "poweroff":
		verb = "poweroff"
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}
	// Reply first, then act a moment later so the client gets confirmation.
	writeJSON(w, map[string]any{"status": "ok", "action": req.Action})
	go func() {
		time.Sleep(time.Second)
		exec.Command("sudo", "-n", "systemctl", verb).Run()
	}()
}

// --- apt (refresh / upgrade), async with status ----------------------------

type aptState struct {
	Running bool   `json:"running"`
	Action  string `json:"action"`
	Status  string `json:"status"` // idle | running | done | error
	Message string `json:"message"`
}

var (
	aptMu sync.Mutex
	apt   = aptState{Status: "idle"}
)

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func startApt(action string, args []string) bool {
	aptMu.Lock()
	if apt.Running {
		aptMu.Unlock()
		return false
	}
	apt = aptState{Running: true, Action: action, Status: "running", Message: action + " in progress…"}
	aptMu.Unlock()

	go func() {
		out, err := exec.Command("sudo", args...).CombinedOutput()
		aptMu.Lock()
		apt.Running = false
		if err != nil {
			apt.Status = "error"
			apt.Message = tail(string(out)+" "+err.Error(), 500)
		} else {
			apt.Status = "done"
			apt.Message = tail(string(out), 500)
		}
		aptMu.Unlock()
	}()
	return true
}

// SystemRefresh — POST /api/system/refresh  (apt-get update)
func SystemRefresh(w http.ResponseWriter, r *http.Request) {
	if !startApt("refresh", []string{"-n", "apt-get", "update"}) {
		http.Error(w, "An apt operation is already running", http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"status": "started"})
}

// SystemUpgrade — POST /api/system/upgrade  (apt-get full-upgrade -y)
func SystemUpgrade(w http.ResponseWriter, r *http.Request) {
	args := []string{"-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "full-upgrade", "-y",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
	if !startApt("upgrade", args) {
		http.Error(w, "An apt operation is already running", http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"status": "started"})
}

// SystemAptStatus — GET /api/system/apt-status
func SystemAptStatus(w http.ResponseWriter, r *http.Request) {
	aptMu.Lock()
	st := apt
	aptMu.Unlock()
	writeJSON(w, st)
}
