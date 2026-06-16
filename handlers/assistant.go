package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"homelab/metrics"
)

// The Assistant is a chat front-end, tightly integrated with this Pi, that
// answers questions about the host using its LIVE state as grounding. Inference
// itself runs on a remote Ollama server (typically the user's main PC, which has
// the CPU/GPU to run the model) — the Pi only gathers context, proxies the
// request, and streams tokens back. Nothing is stored on the Pi.
//
// Three endpoints back the Assistant tab:
//   GET  /api/assistant/config   -> current settings
//   POST /api/assistant/config   -> save settings (persisted to assistant.json)
//   GET  /api/assistant/probe    -> ask Ollama what models it has (readiness)
//   WS   /ws/assistant/chat      -> stream one chat turn, grounded in Pi state

const assistantFile = "assistant.json"

// QuickPrompt is one entry in the assistant's quick-prompts menu: a display name
// and the message sent when chosen. Both are user-editable in the settings panel.
type QuickPrompt struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// AssistantSettings is the user-configurable state for the Assistant tab,
// persisted to assistant.json in the working directory.
type AssistantSettings struct {
	Enabled      bool          `json:"enabled"`
	OllamaURL    string        `json:"ollama_url"`
	Model        string        `json:"model"`
	Title        string        `json:"title"`
	SystemPrompt string        `json:"system_prompt"`
	QuickPrompts []QuickPrompt `json:"quick_prompts"`
}

const defaultSystemPrompt = "You are the assistant built into this Raspberry Pi 5 homelab dashboard, running on Linux. " +
	"Answer the user's question using ONLY the live system data below — never invent numbers. " +
	"Be direct and concise: lead with the answer, cite specific service names, PIDs, container names and figures, and give a brief 'why' only when it adds value. " +
	"When you list things, use a Markdown bullet list (\"- item\"). Do not use emoji. " +
	"Skip filler, hedging and generic disclaimers. Keep a calm, factual tone."

func defaultAssistantSettings() AssistantSettings {
	return AssistantSettings{
		Enabled:      true,
		OllamaURL:    "http://192.168.1.10:11434",
		Model:        "qwen2.5:3b",
		Title:        "Atlas",
		SystemPrompt: defaultSystemPrompt,
		QuickPrompts: []QuickPrompt{
			{Name: "Pi Overview", Prompt: "Give me a detailed overview of this Raspberry Pi right now — CPU, memory, temperature, disk, and network — and call out anything notable."},
			{Name: "Top Processes", Prompt: "List the top processes using the most resources right now. Show each one's CPU% and memory, highest first."},
			{Name: "Health Check", Prompt: "Quick health check: CPU usage and temperature, RAM used and total, disk free, any failed services, and uptime. One short line each."},
		},
	}
}

var (
	assistantMu   sync.RWMutex
	assistant     AssistantSettings
	assistantOnce sync.Once
)

// loadAssistant lazily reads assistant.json, falling back to defaults for any
// missing fields so a partial or absent file still yields a working config.
func loadAssistant() {
	assistantOnce.Do(func() {
		assistant = defaultAssistantSettings()
		if b, err := os.ReadFile(assistantFile); err == nil {
			_ = json.Unmarshal(b, &assistant)
		}
		// Repair empties so the UI never shows a blank, unusable field.
		d := defaultAssistantSettings()
		if strings.TrimSpace(assistant.OllamaURL) == "" {
			assistant.OllamaURL = d.OllamaURL
		}
		if strings.TrimSpace(assistant.Model) == "" {
			assistant.Model = d.Model
		}
		if strings.TrimSpace(assistant.Title) == "" {
			assistant.Title = d.Title
		}
		if strings.TrimSpace(assistant.SystemPrompt) == "" {
			assistant.SystemPrompt = d.SystemPrompt
		}
		if len(assistant.QuickPrompts) == 0 {
			assistant.QuickPrompts = d.QuickPrompts
		}
	})
}

func getAssistant() AssistantSettings {
	loadAssistant()
	assistantMu.RLock()
	defer assistantMu.RUnlock()
	return assistant
}

// AssistantConfig — GET returns the current settings, POST replaces them.
func AssistantConfig(w http.ResponseWriter, r *http.Request) {
	loadAssistant()
	if r.Method == http.MethodPost {
		var in AssistantSettings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad JSON", http.StatusBadRequest)
			return
		}
		// Normalise: trim a trailing slash on the URL so url+"/api/chat" is clean.
		in.OllamaURL = strings.TrimRight(strings.TrimSpace(in.OllamaURL), "/")
		in.Model = strings.TrimSpace(in.Model)
		in.Title = strings.TrimSpace(in.Title)
		d := defaultAssistantSettings()
		if in.OllamaURL == "" {
			in.OllamaURL = d.OllamaURL
		}
		if in.Model == "" {
			in.Model = d.Model
		}
		if in.Title == "" {
			in.Title = d.Title
		}
		if strings.TrimSpace(in.SystemPrompt) == "" {
			in.SystemPrompt = d.SystemPrompt
		}

		assistantMu.Lock()
		assistant = in
		assistantMu.Unlock()

		if b, err := json.MarshalIndent(in, "", "  "); err == nil {
			_ = os.WriteFile(assistantFile, b, 0o600)
		}
	}
	writeJSON(w, getAssistant())
}

// ollamaModel is one entry in Ollama's /api/tags response.
type ollamaTagsResp struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// AssistantProbe — GET /api/assistant/probe. Asks the configured Ollama server
// for its installed models so the UI can tell "server down" from "model not
// installed" and clear/show its setup card accordingly.
func AssistantProbe(w http.ResponseWriter, r *http.Request) {
	s := getAssistant()
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	out := map[string]any{"model": s.Model, "ollama_url": s.OllamaURL, "reachable": false, "ready": false, "models": []string{}}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.OllamaURL+"/api/tags", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		out["error"] = fmt.Sprintf("cannot reach Ollama at %s", s.OllamaURL)
		writeJSON(w, out)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out["error"] = fmt.Sprintf("Ollama returned HTTP %d", resp.StatusCode)
		writeJSON(w, out)
		return
	}
	var tr ollamaTagsResp
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		out["error"] = "could not parse Ollama response"
		writeJSON(w, out)
		return
	}
	names := make([]string, 0, len(tr.Models))
	for _, m := range tr.Models {
		switch {
		case m.Name != "":
			names = append(names, m.Name)
		case m.Model != "":
			names = append(names, m.Model)
		}
	}
	out["reachable"] = true
	out["models"] = names
	out["ready"] = modelInstalled(names, s.Model)
	writeJSON(w, out)
}

// modelInstalled reports whether want is among the Ollama tags. An untagged want
// (e.g. "qwen2.5") matches any of its tags, since Ollama defaults to ":latest".
func modelInstalled(models []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	hasTag := strings.Contains(want, ":")
	for _, m := range models {
		if m == want {
			return true
		}
		if !hasTag && (m == want+":latest" || strings.HasPrefix(m, want+":")) {
			return true
		}
	}
	return false
}

// --- chat over WebSocket -----------------------------------------------------

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatReq struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ollamaChatChunk struct {
	Message       chatMessage `json:"message"`
	Done          bool        `json:"done"`
	Error         string      `json:"error,omitempty"`
	EvalCount     int         `json:"eval_count"`
	EvalDuration  int64       `json:"eval_duration"`  // ns
	TotalDuration int64       `json:"total_duration"` // ns
}

// AssistantChat — WS /ws/assistant/chat. The client sends one frame:
//
//	{"messages":[{"role":"user","content":"..."}, ...]}   (no system message)
//
// The server prepends a system message built from this Pi's live state, streams
// the request to Ollama, and relays frames back:
//
//	{"token":"..."}                                        per chunk
//	{"done":true,"tokens":N,"tok_per_sec":X,"total_s":Y}   final
//	{"error":"..."}                                        on failure
func AssistantChat(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	s := getAssistant()
	if !s.Enabled {
		writeChatErr(conn, "The assistant is disabled. Enable it in the Assistant settings.")
		return
	}

	for {
		_, p, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var in struct {
			Messages []chatMessage `json:"messages"`
		}
		if json.Unmarshal(p, &in) != nil || len(in.Messages) == 0 {
			continue
		}
		// Refresh settings each turn so a save mid-session takes effect.
		s = getAssistant()
		msgs := append([]chatMessage{{Role: "system", Content: s.SystemPrompt + "\n\n" + piContext() + agentActionsPrompt()}}, in.Messages...)
		streamChat(conn, s, msgs)
	}
}

func writeChatErr(conn *websocket.Conn, msg string) {
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = conn.WriteJSON(map[string]any{"error": msg})
}

// streamChat proxies one chat turn to Ollama and relays tokens to the client.
func streamChat(conn *websocket.Conn, s AssistantSettings, msgs []chatMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	body, _ := json.Marshal(ollamaChatReq{Model: s.Model, Messages: msgs, Stream: true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.OllamaURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		writeChatErr(conn, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	hc := &http.Client{Timeout: 3 * time.Minute}
	resp, err := hc.Do(req)
	if err != nil {
		writeChatErr(conn, fmt.Sprintf("cannot reach Ollama at %s — check the URL in settings and that the server is running", s.OllamaURL))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := bufio.NewReader(resp.Body).ReadString('\n')
		writeChatErr(conn, fmt.Sprintf("Ollama returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(b)))
		return
	}

	start := time.Now()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ch ollamaChatChunk
		if json.Unmarshal(line, &ch) != nil {
			continue
		}
		if ch.Error != "" {
			writeChatErr(conn, "Ollama: "+ch.Error)
			return
		}
		if ch.Message.Content != "" {
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if conn.WriteJSON(map[string]any{"token": ch.Message.Content}) != nil {
				return
			}
		}
		if ch.Done {
			tps := 0.0
			if ch.EvalDuration > 0 {
				tps = float64(ch.EvalCount) / (float64(ch.EvalDuration) / 1e9)
			}
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = conn.WriteJSON(map[string]any{
				"done":        true,
				"tokens":      ch.EvalCount,
				"tok_per_sec": tps,
				"total_s":     time.Since(start).Seconds(),
				"model":       s.Model,
			})
			return
		}
	}
	if err := sc.Err(); err != nil {
		writeChatErr(conn, err.Error())
	}
}

// --- live Pi context ---------------------------------------------------------

// piContext assembles the live system snapshot sent as grounding. This is the
// "tight integration": the assistant runs on the Pi and reads its real state —
// metrics, top processes, failed services, containers — directly, no SSH needed.
func piContext() string {
	var b strings.Builder

	b.WriteString("[MACHINE]\n")
	if h, err := os.Hostname(); err == nil {
		fmt.Fprintf(&b, "Hostname: %s\n", h)
	}
	b.WriteString("OS: " + osLabel() + "\n")
	if d, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		fmt.Fprintf(&b, "Model: %s\n", strings.TrimRight(string(d), "\x00"))
	}
	if d, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		fmt.Fprintf(&b, "Kernel: %s\n", strings.TrimSpace(string(d)))
	}
	if ip := primaryIP(); ip != "" {
		fmt.Fprintf(&b, "Primary IP: %s\n", ip)
	}

	m := metrics.GetMetrics()
	if m != nil {
		b.WriteString("\n[HARDWARE — live]\n")
		fmt.Fprintf(&b, "CPU: %.0f%% @ %.0f MHz", m.CpuPercent, m.CpuFreqMHz)
		if m.CpuTempC > 0 {
			fmt.Fprintf(&b, ", %.1f°C", m.CpuTempC)
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, "Load average: %.2f %.2f %.2f (1/5/15 min)\n", m.Load1, m.Load5, m.Load15)
		fmt.Fprintf(&b, "Memory: %d MB used of %d MB (%.0f%%); swap %d MB of %d MB\n",
			m.MemUsedMB, m.MemTotalMB, m.MemPercent, m.SwapUsedMB, m.SwapTotalMB)
		fmt.Fprintf(&b, "Disk %s: %.1f GB used of %.1f GB (%.0f%%)",
			m.DiskName, m.DiskUsedGB, m.DiskTotalGB, m.DiskPercent)
		if m.DiskTempC > 0 {
			fmt.Fprintf(&b, ", %.0f°C", m.DiskTempC)
		}
		fmt.Fprintf(&b, ", read %.1f MB/s, write %.1f MB/s\n", m.DiskReadMBps, m.DiskWriteMBps)
		fmt.Fprintf(&b, "Network: down %.0f Kbps, up %.0f Kbps\n", m.NetRxKbps, m.NetTxKbps)
		if m.FanMode != "" {
			fmt.Fprintf(&b, "Fan: %s, %d%%\n", m.FanMode, m.FanPercent)
		}
		if m.Throttled != "" && m.Throttled != "0x0" {
			fmt.Fprintf(&b, "Throttling flags: %s (non-zero — under-voltage or thermal capping has occurred)\n", m.Throttled)
		}
		fmt.Fprintf(&b, "Uptime: %s\n", humanUptime(m.UptimeSeconds))
	}

	if procs := metrics.GetProcesses(); len(procs) > 0 {
		byCPU := append([]metrics.ProcessInfo(nil), procs...)
		sort.Slice(byCPU, func(i, j int) bool { return byCPU[i].CpuPercent > byCPU[j].CpuPercent })
		fmt.Fprintf(&b, "\n[TOP PROCESSES] %d tracked\n", len(procs))
		b.WriteString("By CPU:\n")
		for _, p := range byCPU[:min(8, len(byCPU))] {
			fmt.Fprintf(&b, "- %s (pid %d): %.0f%% CPU, %.0f MB\n", p.Name, p.PID, p.CpuPercent, p.MemMB)
		}
		byMem := append([]metrics.ProcessInfo(nil), procs...)
		sort.Slice(byMem, func(i, j int) bool { return byMem[i].MemMB > byMem[j].MemMB })
		b.WriteString("By memory:\n")
		for _, p := range byMem[:min(8, len(byMem))] {
			fmt.Fprintf(&b, "- %s (pid %d): %.0f MB, %.0f%% CPU\n", p.Name, p.PID, p.MemMB, p.CpuPercent)
		}
	}

	b.WriteString(servicesContext())
	b.WriteString(dockerContext())
	return b.String()
}

// servicesContext summarises systemd: total/running counts and any failed units.
func servicesContext() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "list-units", "--type=service",
		"--all", "--no-legend", "--no-pager", "--plain").Output()
	if err != nil {
		return ""
	}
	var total, running int
	var failed []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		total++
		switch f[2] {
		case "active":
			if f[3] == "running" {
				running++
			}
		case "failed":
			failed = append(failed, f[0])
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[SERVICES] %d total, %d running, %d failed\n", total, running, len(failed))
	if len(failed) > 0 {
		b.WriteString("Failed: " + strings.Join(failed, ", ") + "\n")
	}
	return b.String()
}

// dockerContext lists running containers (best-effort; empty if Docker is absent).
func dockerContext() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "\n[DOCKER] no running containers\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[DOCKER] %d running container(s)\n", len(lines))
	for _, line := range lines {
		f := strings.Split(line, "\t")
		if len(f) >= 3 {
			fmt.Fprintf(&b, "- %s (%s): %s\n", f[0], f[1], f[2])
		}
	}
	return b.String()
}

func humanUptime(secs int) string {
	d := time.Duration(secs) * time.Second
	days, hours, mins := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
