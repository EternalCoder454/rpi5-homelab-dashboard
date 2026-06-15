package handlers

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// The hub holds two user-managed lists, persisted to hub.json in the working
// directory: service links (tiles on the landing page) and health checks
// (targets the dashboard pings on a schedule, like the original hcheck tool).

const hubFile = "hub.json"

type HubLink struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Icon string `json:"icon"` // emoji or single char, optional
}

type HealthCheck struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Target string `json:"target"` // http(s) URL, or host:port for a TCP check
}

type hubData struct {
	Links  []HubLink     `json:"links"`
	Checks []HealthCheck `json:"checks"`
}

type checkStatus struct {
	Up        bool  `json:"up"`
	Code      int   `json:"code"`
	LatencyMs int64 `json:"latency_ms"`
	LastCheck int64 `json:"last_check"`
}

var (
	hubMu     sync.RWMutex
	hub       hubData
	hubOnce   sync.Once
	healthMu  sync.RWMutex
	healthMap = map[string]checkStatus{}
)

// health checks tolerate self-signed certs — homelab services often use them.
var healthClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
}

// defaultHub seeds a few working links + checks so a fresh dashboard isn't
// empty. The user can edit or delete any of them.
func defaultHub() hubData {
	return hubData{
		Links: []HubLink{
			{ID: randID(), Name: "Router", URL: "http://192.168.1.1", Icon: "🌐"},
			{ID: randID(), Name: "Pi Docs", URL: "https://www.raspberrypi.com/documentation/", Icon: "📚"},
			{ID: randID(), Name: "Pi Forums", URL: "https://forums.raspberrypi.com", Icon: "💬"},
			{ID: randID(), Name: "Docker Hub", URL: "https://hub.docker.com", Icon: "🐳"},
		},
		Checks: []HealthCheck{
			{ID: randID(), Name: "Gateway", Target: "http://192.168.1.1"},
			{ID: randID(), Name: "Internet", Target: "https://1.1.1.1"},
		},
	}
}

// StartHub loads persisted data and begins the background health poller.
func StartHub() {
	hubOnce.Do(func() {
		if data, err := os.ReadFile(hubFile); err == nil {
			hubMu.Lock()
			json.Unmarshal(data, &hub)
			hubMu.Unlock()
		} else if os.IsNotExist(err) {
			// First run: seed sensible defaults so the hub isn't blank.
			hubMu.Lock()
			hub = defaultHub()
			saveHubLocked()
			hubMu.Unlock()
		}
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				collectHealth()
				<-t.C
			}
		}()
	})
}

func collectHealth() {
	hubMu.RLock()
	checks := append([]HealthCheck(nil), hub.Checks...)
	hubMu.RUnlock()

	live := map[string]bool{}
	for _, c := range checks {
		live[c.ID] = true
		up, code, lat := pingTarget(c.Target)
		healthMu.Lock()
		healthMap[c.ID] = checkStatus{Up: up, Code: code, LatencyMs: lat, LastCheck: time.Now().Unix()}
		healthMu.Unlock()
	}
	// drop status for removed checks
	healthMu.Lock()
	for id := range healthMap {
		if !live[id] {
			delete(healthMap, id)
		}
	}
	healthMu.Unlock()
}

// pingTarget returns whether the target responded, the HTTP status (0 for TCP),
// and the round-trip latency in ms. "Up" means we got any response at all — a
// service returning 401/403 is still reachable.
func pingTarget(target string) (bool, int, int64) {
	start := time.Now()
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		resp, err := healthClient.Get(target)
		lat := time.Since(start).Milliseconds()
		if err != nil {
			return false, 0, lat
		}
		resp.Body.Close()
		return true, resp.StatusCode, lat
	}
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return false, 0, lat
	}
	conn.Close()
	return true, 0, lat
}

func saveHubLocked() error {
	data, _ := json.MarshalIndent(hub, "", "  ")
	tmp, err := os.CreateTemp(".", ".hub-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	tmp.Write(data)
	tmp.Sync()
	tmp.Close()
	return os.Rename(name, hubFile)
}

// HubGet — GET /api/hub
func HubGet(w http.ResponseWriter, r *http.Request) {
	hubMu.RLock()
	links := append([]HubLink(nil), hub.Links...)
	checks := append([]HealthCheck(nil), hub.Checks...)
	hubMu.RUnlock()

	type checkView struct {
		HealthCheck
		checkStatus
	}
	healthMu.RLock()
	views := make([]checkView, 0, len(checks))
	for _, c := range checks {
		views = append(views, checkView{HealthCheck: c, checkStatus: healthMap[c.ID]})
	}
	healthMu.RUnlock()

	if links == nil {
		links = []HubLink{}
	}
	writeJSON(w, map[string]any{"links": links, "checks": views})
}

// HubLinkAdd — POST /api/hub/link/add {name, url, icon}
func HubLinkAdd(w http.ResponseWriter, r *http.Request) {
	var l HubLink
	if err := json.NewDecoder(r.Body).Decode(&l); err != nil || strings.TrimSpace(l.Name) == "" || strings.TrimSpace(l.URL) == "" {
		http.Error(w, "name and url required", http.StatusBadRequest)
		return
	}
	l.ID = randID()
	hubMu.Lock()
	hub.Links = append(hub.Links, l)
	err := saveHubLocked()
	hubMu.Unlock()
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "id": l.ID})
}

// HubCheckAdd — POST /api/hub/check/add {name, target}
func HubCheckAdd(w http.ResponseWriter, r *http.Request) {
	var c HealthCheck
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Target) == "" {
		http.Error(w, "name and target required", http.StatusBadRequest)
		return
	}
	c.ID = randID()
	hubMu.Lock()
	hub.Checks = append(hub.Checks, c)
	err := saveHubLocked()
	hubMu.Unlock()
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	// Prime its status immediately so the UI doesn't wait a full cycle.
	up, code, lat := pingTarget(c.Target)
	healthMu.Lock()
	healthMap[c.ID] = checkStatus{Up: up, Code: code, LatencyMs: lat, LastCheck: time.Now().Unix()}
	healthMu.Unlock()
	writeJSON(w, map[string]any{"status": "ok", "id": c.ID})
}

// HubLinkUpdate — POST /api/hub/link/update {id, name, url, icon}
func HubLinkUpdate(w http.ResponseWriter, r *http.Request) {
	var in HubLink
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID == "" ||
		strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.URL) == "" {
		http.Error(w, "id, name and url required", http.StatusBadRequest)
		return
	}
	hubMu.Lock()
	found := false
	for i := range hub.Links {
		if hub.Links[i].ID == in.ID {
			hub.Links[i].Name = in.Name
			hub.Links[i].URL = in.URL
			hub.Links[i].Icon = in.Icon
			found = true
			break
		}
	}
	var err error
	if found {
		err = saveHubLocked()
	}
	hubMu.Unlock()
	if !found {
		http.Error(w, "link not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// HubCheckUpdate — POST /api/hub/check/update {id, name, target}
func HubCheckUpdate(w http.ResponseWriter, r *http.Request) {
	var in HealthCheck
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID == "" ||
		strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Target) == "" {
		http.Error(w, "id, name and target required", http.StatusBadRequest)
		return
	}
	hubMu.Lock()
	found := false
	for i := range hub.Checks {
		if hub.Checks[i].ID == in.ID {
			hub.Checks[i].Name = in.Name
			hub.Checks[i].Target = in.Target
			found = true
			break
		}
	}
	var err error
	if found {
		err = saveHubLocked()
	}
	hubMu.Unlock()
	if !found {
		http.Error(w, "check not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	// Re-ping the (possibly new) target so the UI reflects it without waiting
	// for the next 30s poll cycle.
	up, code, lat := pingTarget(in.Target)
	healthMu.Lock()
	healthMap[in.ID] = checkStatus{Up: up, Code: code, LatencyMs: lat, LastCheck: time.Now().Unix()}
	healthMu.Unlock()
	writeJSON(w, map[string]any{"status": "ok"})
}

// HubRemove — POST /api/hub/link/remove or /api/hub/check/remove {id}
func HubRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	isCheck := strings.Contains(r.URL.Path, "/check/")
	hubMu.Lock()
	if isCheck {
		out := hub.Checks[:0]
		for _, c := range hub.Checks {
			if c.ID != req.ID {
				out = append(out, c)
			}
		}
		hub.Checks = out
	} else {
		out := hub.Links[:0]
		for _, l := range hub.Links {
			if l.ID != req.ID {
				out = append(out, l)
			}
		}
		hub.Links = out
	}
	err := saveHubLocked()
	hubMu.Unlock()
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}
