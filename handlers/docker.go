package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	volumetypes "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/gorilla/websocket"
)

// minMemMB is the floor for a container's memory cap. Docker itself allows down
// to ~6MB, but anything that small is unusable in practice.
const minMemMB = 16

// --- client -----------------------------------------------------------------

var (
	dockerCli  *client.Client
	dockerErr  error
	dockerOnce sync.Once
)

func dockerClient() (*client.Client, error) {
	dockerOnce.Do(func() {
		dockerCli, dockerErr = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	})
	return dockerCli, dockerErr
}

func randID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- stats collector --------------------------------------------------------
//
// A single goroutine polls one-shot stats for every running container every 3s
// and caches CPU%/memory. ContainerStatsOneShot leaves PreCPUStats zero, so we
// keep each container's previous CPUStats here and compute the delta between
// our own ticks. dPrevCPU is owned solely by the collector (no lock needed);
// only dStats is shared with request handlers.

type cStat struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
}

var (
	dStatsMu sync.RWMutex
	dStats   = map[string]cStat{}
	dPrevCPU = map[string]container.CPUStats{}
)

// StartDocker launches the stats collector. It is a no-op at runtime if Docker
// is not reachable (the calls just error and we retry next tick).
func StartDocker() {
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			collectDockerStats()
			<-t.C
		}
	}()
}

func collectDockerStats() {
	cli, err := dockerClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	list, err := cli.ContainerList(ctx, container.ListOptions{}) // running only
	if err != nil {
		return
	}

	next := make(map[string]cStat, len(list))
	for _, c := range list {
		resp, err := cli.ContainerStatsOneShot(ctx, c.ID)
		if err != nil {
			continue
		}
		var s container.StatsResponse
		derr := json.NewDecoder(resp.Body).Decode(&s)
		resp.Body.Close()
		if derr != nil {
			continue
		}
		cpu := 0.0
		if prev, ok := dPrevCPU[c.ID]; ok {
			cpu = calcCPU(prev, s.CPUStats)
		}
		dPrevCPU[c.ID] = s.CPUStats
		next[c.ID] = cStat{CPUPercent: cpu, MemUsage: memUsage(s.MemoryStats), MemLimit: s.MemoryStats.Limit}
	}

	// Drop delta state for containers that are gone.
	for id := range dPrevCPU {
		if _, ok := next[id]; !ok {
			delete(dPrevCPU, id)
		}
	}

	dStatsMu.Lock()
	dStats = next
	dStatsMu.Unlock()
}

func calcCPU(prev, cur container.CPUStats) float64 {
	cpuDelta := float64(cur.CPUUsage.TotalUsage) - float64(prev.CPUUsage.TotalUsage)
	sysDelta := float64(cur.SystemUsage) - float64(prev.SystemUsage)
	onlineCPUs := float64(cur.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(cur.CPUUsage.PercpuUsage))
	}
	if sysDelta > 0 && cpuDelta > 0 {
		return (cpuDelta / sysDelta) * onlineCPUs * 100.0
	}
	return 0
}

func memUsage(m container.MemoryStats) uint64 {
	if v, ok := m.Stats["total_inactive_file"]; ok && v < m.Usage {
		return m.Usage - v
	}
	if v, ok := m.Stats["inactive_file"]; ok && v < m.Usage {
		return m.Usage - v
	}
	return m.Usage
}

// --- list -------------------------------------------------------------------

type containerView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Image      string  `json:"image"`
	State      string  `json:"state"`
	Status     string  `json:"status"`
	Ports      string  `json:"ports"`
	CPUPercent float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
}

// DockerList — GET /api/docker/containers
func DockerList(w http.ResponseWriter, r *http.Request) {
	cli, err := dockerClient()
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	list, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}

	dStatsMu.RLock()
	stats := dStats
	views := make([]containerView, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		st := stats[c.ID]
		views = append(views, containerView{
			ID:         c.ID,
			Name:       name,
			Image:      c.Image,
			State:      string(c.State),
			Status:     c.Status,
			Ports:      formatPorts(c.Ports),
			CPUPercent: st.CPUPercent,
			MemUsage:   st.MemUsage,
			MemLimit:   st.MemLimit,
		})
	}
	dStatsMu.RUnlock()

	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	writeJSON(w, map[string]any{"available": true, "containers": views})
}

func formatPorts(ports []container.Port) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range ports {
		var s string
		if p.PublicPort > 0 {
			s = fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type)
		} else {
			s = fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, ", ")
}

// --- create (async pull) ----------------------------------------------------

type pullState struct {
	Status      string `json:"status"` // pulling | creating | done | error
	Message     string `json:"message"`
	ContainerID string `json:"container_id"`
}

var (
	pullMu sync.Mutex
	pulls  = map[string]pullState{}
)

func setPull(id, status, msg, cid string) {
	pullMu.Lock()
	pulls[id] = pullState{Status: status, Message: msg, ContainerID: cid}
	pullMu.Unlock()
}

// DockerCreate — POST /api/docker/create {name, image, memory_mb, ports, env}
// Returns immediately; the pull + create + start run in the background. The
// frontend polls DockerPullStatus with the returned pull_id.
func DockerCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string   `json:"name"`
		Image    string   `json:"image"`
		MemoryMB int      `json:"memory_mb"`
		Ports    []string `json:"ports"`
		Env      []string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	req.Image = strings.TrimSpace(req.Image)
	if req.Image == "" {
		http.Error(w, "Image is required", http.StatusBadRequest)
		return
	}
	if req.MemoryMB < minMemMB {
		http.Error(w, fmt.Sprintf("Memory must be at least %d MB", minMemMB), http.StatusBadRequest)
		return
	}
	cli, err := dockerClient()
	if err != nil {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}

	id := randID()
	setPull(id, "pulling", "Pulling "+req.Image, "")
	writeJSON(w, map[string]any{"status": "pulling", "pull_id": id})

	go func() {
		ctx := context.Background()

		// ImagePull is async: the image is not actually present until the
		// response body has been fully read. io.Copy to Discard is REQUIRED.
		reader, err := cli.ImagePull(ctx, req.Image, imagetypes.PullOptions{})
		if err != nil {
			setPull(id, "error", "pull failed: "+err.Error(), "")
			return
		}
		io.Copy(io.Discard, reader)
		reader.Close()

		setPull(id, "creating", "Creating container", "")

		exposed, bindings, err := nat.ParsePortSpecs(req.Ports)
		if err != nil {
			setPull(id, "error", "invalid ports: "+err.Error(), "")
			return
		}

		cfg := &container.Config{
			Image:        req.Image,
			Env:          req.Env,
			ExposedPorts: exposed,
		}
		host := &container.HostConfig{
			Resources:     container.Resources{Memory: int64(req.MemoryMB) << 20},
			PortBindings:  bindings,
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		}

		created, err := cli.ContainerCreate(ctx, cfg, host, nil, nil, req.Name)
		if err != nil {
			setPull(id, "error", "create failed: "+err.Error(), "")
			return
		}
		if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			setPull(id, "error", "created but failed to start: "+err.Error(), created.ID)
			return
		}
		setPull(id, "done", "Container running", created.ID)
	}()
}

// DockerPullStatus — GET /api/docker/pull/status?id=
func DockerPullStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	pullMu.Lock()
	st, ok := pulls[id]
	// Clean up terminal states once observed so the map doesn't grow.
	if ok && (st.Status == "done" || st.Status == "error") {
		delete(pulls, id)
	}
	pullMu.Unlock()
	if !ok {
		writeJSON(w, map[string]any{"status": "unknown"})
		return
	}
	writeJSON(w, st)
}

// --- actions ----------------------------------------------------------------

// DockerAction — POST /api/docker/action {id, action}
func DockerAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Action string `json:"action"` // start | stop | restart | remove
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	cli, err := dockerClient()
	if err != nil {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	switch req.Action {
	case "start":
		err = cli.ContainerStart(ctx, req.ID, container.StartOptions{})
	case "stop":
		err = cli.ContainerStop(ctx, req.ID, container.StopOptions{})
	case "restart":
		err = cli.ContainerRestart(ctx, req.ID, container.StopOptions{})
	case "remove":
		// Force-remove the container and its anonymous volumes. VolumeRemove
		// elsewhere uses force=true for the same reason: a just-stopped
		// container can still hold a reference.
		err = cli.ContainerRemove(ctx, req.ID, container.RemoveOptions{RemoveVolumes: true, Force: true})
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// DockerTop — GET /api/docker/top?id=  (processes running inside a container)
func DockerTop(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	cli, err := dockerClient()
	if err != nil || id == "" {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	top, err := cli.ContainerTop(ctx, id, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"titles": top.Titles, "processes": top.Processes})
}

// --- images -----------------------------------------------------------------

// DockerImages — GET /api/docker/images
func DockerImages(w http.ResponseWriter, r *http.Request) {
	cli, err := dockerClient()
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	imgs, err := cli.ImageList(ctx, imagetypes.ListOptions{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	type imgView struct {
		ID      string `json:"id"`
		Repo    string `json:"repo"`
		Size    int64  `json:"size"`
		Created int64  `json:"created"`
	}
	out := make([]imgView, 0, len(imgs))
	for _, im := range imgs {
		repo := "<none>"
		if len(im.RepoTags) > 0 {
			repo = im.RepoTags[0]
		}
		id := strings.TrimPrefix(im.ID, "sha256:")
		if len(id) > 12 {
			id = id[:12]
		}
		out = append(out, imgView{ID: id, Repo: repo, Size: im.Size, Created: im.Created})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	writeJSON(w, map[string]any{"available": true, "images": out})
}

// DockerImageRemove — POST /api/docker/images/remove {id}
func DockerImageRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	cli, err := dockerClient()
	if err != nil {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := cli.ImageRemove(ctx, req.ID, imagetypes.RemoveOptions{Force: true, PruneChildren: true}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// --- volumes ----------------------------------------------------------------

// DockerVolumes — GET /api/docker/volumes
func DockerVolumes(w http.ResponseWriter, r *http.Request) {
	cli, err := dockerClient()
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := cli.VolumeList(ctx, volumetypes.ListOptions{})
	if err != nil {
		writeJSON(w, map[string]any{"available": false, "error": err.Error()})
		return
	}
	type volView struct {
		Name       string `json:"name"`
		Driver     string `json:"driver"`
		Mountpoint string `json:"mountpoint"`
		Created    string `json:"created"`
	}
	out := make([]volView, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		out = append(out, volView{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: v.CreatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, map[string]any{"available": true, "volumes": out})
}

// DockerVolumeRemove — POST /api/docker/volumes/remove {name}
func DockerVolumeRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	cli, err := dockerClient()
	if err != nil {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	// force=true so a volume still referenced by a recently-stopped container
	// can be removed (matches the spec for project deletion).
	if err := cli.VolumeRemove(ctx, req.Name, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// --- logs (WebSocket) -------------------------------------------------------

type wsTextWriter struct{ conn *websocket.Conn }

func (w wsTextWriter) Write(p []byte) (int, error) {
	w.conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := w.conn.WriteMessage(websocket.TextMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// DockerLogs — WS /ws/docker/logs?id=  (live-follows the last 200 lines)
func DockerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	cli, err := dockerClient()
	if err != nil || id == "" {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel as soon as the browser closes the socket.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	logs, err := cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "200",
	})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte("error: "+err.Error()))
		return
	}
	defer logs.Close()

	// Containers created here have no TTY, so the log stream is multiplexed;
	// stdcopy demuxes stdout+stderr into the single WebSocket text stream.
	out := wsTextWriter{conn}
	stdcopy.StdCopy(out, out, logs)
}

// --- terminal (WebSocket exec) ----------------------------------------------

// DockerTerminal — WS /ws/docker/terminal?id=  (interactive shell via exec).
// Same message protocol as the system terminal: {type:input,data} / {rows,cols}.
func DockerTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	cli, err := dockerClient()
	if err != nil || id == "" {
		http.Error(w, "Docker unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx := context.Background()
	execID, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          []string{"/bin/sh"},
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		conn.WriteMessage(websocket.BinaryMessage, []byte("exec create failed: "+err.Error()))
		return
	}
	hijack, err := cli.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		conn.WriteMessage(websocket.BinaryMessage, []byte("exec attach failed: "+err.Error()))
		return
	}
	defer hijack.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	// exec output -> WS (binary frames, like the PTY terminal)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := hijack.Reader.Read(buf)
			if n > 0 {
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// WS -> exec stdin; closing the hijacked conn unblocks the reader above.
	for {
		mt, p, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.TextMessage {
			var msg struct {
				Type string `json:"type"`
				Data string `json:"data"`
				Rows int    `json:"rows"`
				Cols int    `json:"cols"`
			}
			if json.Unmarshal(p, &msg) != nil {
				continue
			}
			switch {
			case msg.Type == "input":
				hijack.Conn.Write([]byte(msg.Data))
			case msg.Type == "resize" || msg.Rows > 0:
				cli.ContainerExecResize(ctx, execID.ID, container.ResizeOptions{
					Height: uint(msg.Rows),
					Width:  uint(msg.Cols),
				})
			}
		} else if mt == websocket.BinaryMessage {
			hijack.Conn.Write(p)
		}
	}

	hijack.Close()
	wg.Wait()
}
