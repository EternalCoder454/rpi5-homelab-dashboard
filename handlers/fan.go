package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// The Argon fan is driven by the argononed daemon, which re-applies its curve
// (/etc/argononed.conf) every ~30s and also services the power button. So
// rather than fight it over I2C, manual control is expressed as a fixed-speed
// curve the daemon itself applies. A safety entry forces 100% near the throttle
// point so a low manual setting can never cook the Pi. argononed is reloaded so
// it picks up the change; the power button is unaffected beyond a ~1s restart.
const (
	argonConfPath   = "/etc/argononed.conf"  // world-writable on Argon installs
	argonAutoBackup = "argon-auto-curve.bak" // saved auto curve (relative to workdir)
	fanManualMarker = "# dashboard-manual"
	fanSafetyTempC  = 75 // force 100% at/above this temperature, even in manual
)

func manualCurve(pct int) string {
	return "# Argon Fan Speed Configuration (CPU)\n" + fanManualMarker + "\n" +
		"0=" + strconv.Itoa(pct) + "\n" +
		strconv.Itoa(fanSafetyTempC) + "=100\n"
}

func defaultCurve() string {
	return "#\n# Argon Fan Speed Configuration (CPU)\n#\n55=30\n60=55\n65=100\n"
}

func restartArgon() error {
	return exec.Command("sudo", "-n", "systemctl", "restart", "argononed").Run()
}

// FanSet — POST /api/fan/set {"percent": N}. Pins the fan to a fixed speed.
func FanSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Percent int `json:"percent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Percent < 0 {
		req.Percent = 0
	}
	if req.Percent > 100 {
		req.Percent = 100
	}
	// Preserve the existing automatic curve the first time we switch to manual.
	if cur, err := os.ReadFile(argonConfPath); err == nil && !strings.Contains(string(cur), fanManualMarker) {
		_ = os.WriteFile(argonAutoBackup, cur, 0o644)
	}
	if err := os.WriteFile(argonConfPath, []byte(manualCurve(req.Percent)), 0o644); err != nil {
		http.Error(w, "Could not write fan config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := restartArgon(); err != nil {
		http.Error(w, "Could not reload fan service: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "mode": "manual", "percent": req.Percent})
}

// FanAuto — POST /api/fan/auto. Restores the automatic temperature curve.
func FanAuto(w http.ResponseWriter, r *http.Request) {
	content := defaultCurve()
	if bak, err := os.ReadFile(argonAutoBackup); err == nil && len(bak) > 0 {
		content = string(bak)
	}
	if err := os.WriteFile(argonConfPath, []byte(content), 0o644); err != nil {
		http.Error(w, "Could not write fan config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := restartArgon(); err != nil {
		http.Error(w, "Could not reload fan service: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "mode": "auto"})
}
