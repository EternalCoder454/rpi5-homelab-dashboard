package handlers

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

// The Updates section lists each upgradable package individually so the user
// can see what will change and upgrade them one at a time, all at once, or just
// re-check. The apt run itself reuses the single-flight runner in system.go
// (startApt / aptState), so an update here and an upgrade elsewhere can't collide.

// UpgradablePkg is one package that has a newer version available.
type UpgradablePkg struct {
	Name      string `json:"name"`
	Current   string `json:"current"`
	Candidate string `json:"candidate"`
	Arch      string `json:"arch"`
	Security  bool   `json:"security"`
	Summary   string `json:"summary"`
}

// pkgNameRe guards anything passed to apt-get install. Debian package names are
// lowercase alphanumerics plus + - . — but membership in the live upgradable set
// is the real check; this is belt-and-suspenders against odd input.
var pkgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]*$`)

// aptUpgradable parses `apt list --upgradable` (cached lists, no root needed).
// Example line:
//
//	vim/stable-security 2:9.0.1-1 arm64 [upgradable from: 2:9.0.0-1]
func aptUpgradable() []UpgradablePkg {
	out, err := exec.Command("apt", "list", "--upgradable").Output()
	if err != nil {
		return nil
	}
	var pkgs []UpgradablePkg
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Listing") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.Contains(fields[0], "/") {
			continue
		}
		nameSuite := strings.SplitN(fields[0], "/", 2)
		p := UpgradablePkg{
			Name:      nameSuite[0],
			Candidate: fields[1],
			Arch:      fields[2],
			Security:  strings.Contains(nameSuite[1], "security"),
		}
		if i := strings.Index(line, "upgradable from:"); i >= 0 {
			p.Current = strings.Trim(strings.TrimSpace(line[i+len("upgradable from:"):]), "]")
		}
		pkgs = append(pkgs, p)
	}
	addSummaries(pkgs)
	return pkgs
}

// addSummaries fills each package's one-line description from apt-cache, in a
// single batched call.
func addSummaries(pkgs []UpgradablePkg) {
	if len(pkgs) == 0 {
		return
	}
	names := make([]string, len(pkgs))
	for i, p := range pkgs {
		names[i] = p.Name
	}
	out, err := exec.Command("apt-cache", append([]string{"--no-all-versions", "show"}, names...)...).Output()
	if err != nil {
		return
	}
	desc := map[string]string{}
	var cur string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "Package:"):
			cur = strings.TrimSpace(strings.TrimPrefix(line, "Package:"))
		case cur != "" && desc[cur] == "" && (strings.HasPrefix(line, "Description:") || strings.HasPrefix(line, "Description-en:")):
			desc[cur] = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	for i := range pkgs {
		pkgs[i].Summary = desc[pkgs[i].Name]
	}
}

// SystemUpdates — GET /api/system/updates. The full upgradable list with detail.
func SystemUpdates(w http.ResponseWriter, r *http.Request) {
	pkgs := aptUpgradable()
	sec := 0
	for _, p := range pkgs {
		if p.Security {
			sec++
		}
	}
	if pkgs == nil {
		pkgs = []UpgradablePkg{}
	}
	writeJSON(w, map[string]any{"packages": pkgs, "count": len(pkgs), "security": sec})
}

// SystemUpdatePackage — POST /api/system/update-package {"name":"<pkg>"}.
// Upgrades exactly one package. The name must be in the current upgradable set
// (and match pkgNameRe) — we never pass arbitrary input to apt.
func SystemUpdatePackage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if !pkgNameRe.MatchString(name) {
		http.Error(w, "Invalid package name", http.StatusBadRequest)
		return
	}
	upgradable := false
	for _, p := range aptUpgradable() {
		if p.Name == name {
			upgradable = true
			break
		}
	}
	if !upgradable {
		http.Error(w, "Package is not in the upgradable list", http.StatusBadRequest)
		return
	}
	args := []string{"-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "--only-upgrade",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", name}
	if !startApt("update "+name, args) {
		http.Error(w, "An apt operation is already running", http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"status": "started"})
}
