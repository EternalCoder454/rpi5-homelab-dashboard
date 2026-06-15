package metrics

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// SystemMetrics is a point-in-time snapshot of host health.
type SystemMetrics struct {
	CpuPercent    float64 `json:"cpu_percent"`
	CpuFreqMHz    float64 `json:"cpu_freq_mhz"`
	CpuTempC      float64 `json:"cpu_temp_c"`
	MemTotalMB    int     `json:"mem_total_mb"`
	MemUsedMB     int     `json:"mem_used_mb"`
	MemPercent    float64 `json:"mem_percent"`
	SwapTotalMB   int     `json:"swap_total_mb"`
	SwapUsedMB    int     `json:"swap_used_mb"`
	DiskTotalGB   float64 `json:"disk_total_gb"`
	DiskUsedGB    float64 `json:"disk_used_gb"`
	DiskPercent   float64 `json:"disk_percent"`
	NetRxKbps     float64 `json:"net_rx_kbps"`
	NetTxKbps     float64 `json:"net_tx_kbps"`
	Load1         float64 `json:"load_1"`
	Load5         float64 `json:"load_5"`
	Load15        float64 `json:"load_15"`
	UptimeSeconds int     `json:"uptime_seconds"`
	FanState      int     `json:"fan_state"`
	Throttled     string  `json:"throttled"`
}

// ProcessInfo is a single entry in the top-processes list.
type ProcessInfo struct {
	PID        int     `json:"pid"`
	Name       string  `json:"name"`
	CpuPercent float64 `json:"cpu_percent"`
	MemMB      float64 `json:"mem_mb"`
}

const userHz = 100 // CONFIG_HZ on Raspberry Pi OS arm64

// --- Shared cache -----------------------------------------------------------
//
// A single background goroutine refreshes the readings every 2s. All HTTP and
// WebSocket handlers read from this cache instead of hitting /proc and /sys
// themselves, so the cost is O(1) per tick regardless of how many clients are
// connected. The delta-tracking state below is therefore only ever touched by
// that one goroutine and needs no locking.

var (
	cacheMu         sync.RWMutex
	cachedMetrics   = &SystemMetrics{}
	cachedProcesses []ProcessInfo

	startOnce sync.Once

	// Delta state, owned exclusively by the collector goroutine.
	lastStat     []uint64
	lastTime     time.Time
	lastRx       uint64
	lastTx       uint64
	lastNetTime  time.Time
	lastProcCPU  = map[int]uint64{}
	lastProcTime time.Time
)

// Start launches the background collectors exactly once. Safe to call multiple
// times.
//
// Metrics and processes run on separate cadences: the cheap /proc and /sys
// reads refresh every 2s, while the much heavier per-PID scan (reading
// /proc/<pid>/stat, status and cmdline for every process) only runs every 5s.
// The process list doesn't need sub-5s granularity and the slower cadence
// noticeably cuts CPU and garbage on the Pi. Each collector owns its own delta
// state, so no locking is needed beyond guarding the two cache pointers.
func Start() {
	startOnce.Do(func() {
		// Prime both caches before the first client connects. Metrics is read
		// twice ~200ms apart so the very first cached snapshot already has real
		// CPU% and network deltas instead of zeros on the initial paint.
		setMetrics(readMetrics())
		time.Sleep(200 * time.Millisecond)
		setMetrics(readMetrics())
		setProcesses(readProcesses())

		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for range t.C {
				setMetrics(readMetrics())
			}
		}()
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for range t.C {
				setProcesses(readProcesses())
			}
		}()
	})
}

func setMetrics(m *SystemMetrics) {
	cacheMu.Lock()
	cachedMetrics = m
	cacheMu.Unlock()
}

func setProcesses(p []ProcessInfo) {
	cacheMu.Lock()
	cachedProcesses = p
	cacheMu.Unlock()
}

// GetMetrics returns the most recent cached system snapshot.
func GetMetrics() *SystemMetrics {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cachedMetrics
}

// GetProcesses returns the most recent cached top-processes list.
func GetProcesses() []ProcessInfo {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return cachedProcesses
}

// --- Collectors -------------------------------------------------------------

func readMetrics() *SystemMetrics {
	m := &SystemMetrics{}

	if cpu, err := readProcStat(); err == nil && len(cpu) >= 5 {
		now := time.Now()
		interval := now.Sub(lastTime).Seconds()
		if lastStat != nil && interval > 0 {
			var totalDelta, idleDelta uint64
			for i := range cpu {
				if i < len(lastStat) && cpu[i] >= lastStat[i] {
					totalDelta += cpu[i] - lastStat[i]
				}
			}
			// idle (index 3) + iowait (index 4)
			idleDelta = (cpu[3] - lastStat[3]) + (cpu[4] - lastStat[4])
			if totalDelta > 0 {
				m.CpuPercent = float64(totalDelta-idleDelta) / float64(totalDelta) * 100
			}
		}
		if m.CpuPercent > 100 {
			m.CpuPercent = 100
		}
		if m.CpuPercent < 0 {
			m.CpuPercent = 0
		}
		lastStat = cpu
		lastTime = now
	}

	readUptime(m)
	readFreq(m)
	readTemp(m)
	readMeminfo(m)
	readDisk(m)
	readNetDev(m)
	readLoadAvg(m)
	readFanState(m)
	readThrottled(m)
	return m
}

// throttled state changes rarely, so cache it for 10s rather than spawning
// vcgencmd on every 2s metrics tick. State is owned by the metrics goroutine.
var (
	throttledCache string
	throttledAt    time.Time
)

func readThrottled(m *SystemMetrics) {
	if !throttledAt.IsZero() && time.Since(throttledAt) < 10*time.Second {
		m.Throttled = throttledCache
		return
	}
	out, err := exec.Command("vcgencmd", "get_throttled").Output()
	if err == nil {
		throttledCache = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "throttled="))
	}
	throttledAt = time.Now()
	m.Throttled = throttledCache
}

func readProcStat() ([]uint64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, fmt.Errorf("unexpected /proc/stat format")
	}
	vals := make([]uint64, 0, len(fields)-1)
	for _, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		vals = append(vals, v)
	}
	return vals, nil
}

func readUptime(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return
	}
	var up float64
	fmt.Sscanf(string(data), "%f", &up)
	m.UptimeSeconds = int(up)
}

func readFreq(m *SystemMetrics) {
	data, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq")
	if err != nil {
		return
	}
	val, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	m.CpuFreqMHz = float64(val) / 1000.0
}

func readTemp(m *SystemMetrics) {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return
	}
	m.CpuTempC = val / 1000.0
}

func readMeminfo(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	vals := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		v, _ := strconv.ParseInt(fields[1], 10, 64)
		vals[key] = v // kB
	}
	m.MemTotalMB = int(vals["MemTotal"] / 1024)
	avail := int(vals["MemAvailable"] / 1024)
	m.MemUsedMB = m.MemTotalMB - avail
	if m.MemTotalMB > 0 {
		m.MemPercent = float64(m.MemUsedMB) / float64(m.MemTotalMB) * 100
	}
	m.SwapTotalMB = int(vals["SwapTotal"] / 1024)
	m.SwapUsedMB = int((vals["SwapTotal"] - vals["SwapFree"]) / 1024)
}

func readDisk(m *SystemMetrics) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return
	}
	total := float64(stat.Blocks) * float64(stat.Bsize) / 1e9
	free := float64(stat.Bfree) * float64(stat.Bsize) / 1e9
	m.DiskTotalGB = total
	m.DiskUsedGB = total - free
	if total > 0 {
		m.DiskPercent = (total - free) / total * 100
	}
}

func readNetDev(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return
	}
	var rxTotal, txTotal uint64
	for _, line := range strings.Split(string(data), "\n") {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:idx])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[idx+1:])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		rxTotal += rx
		txTotal += tx
	}

	now := time.Now()
	interval := now.Sub(lastNetTime).Seconds()
	if !lastNetTime.IsZero() && interval > 0 {
		if rxTotal >= lastRx {
			m.NetRxKbps = float64(rxTotal-lastRx) * 8 / 1000 / interval
		}
		if txTotal >= lastTx {
			m.NetTxKbps = float64(txTotal-lastTx) * 8 / 1000 / interval
		}
	}
	lastRx, lastTx, lastNetTime = rxTotal, txTotal, now
}

func readLoadAvg(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fmt.Sscanf(string(data), "%f %f %f", &m.Load1, &m.Load5, &m.Load15)
}

func readFanState(m *SystemMetrics) {
	data, err := os.ReadFile("/sys/class/thermal/cooling_device0/cur_state")
	if err != nil {
		return
	}
	m.FanState, _ = strconv.Atoi(strings.TrimSpace(string(data)))
}

// --- Processes --------------------------------------------------------------

func readProcesses() []ProcessInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	now := time.Now()
	interval := now.Sub(lastProcTime).Seconds()
	lastProcTime = now

	curr := make(map[int]uint64)
	var procs []ProcessInfo

	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || name[0] < '0' || name[0] > '9' {
			continue
		}
		pid, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", name)

		statData, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		comm, utime, stime, ok := parseStat(statData)
		if !ok {
			continue
		}
		total := utime + stime
		curr[pid] = total

		var cpuPercent float64
		if prev, found := lastProcCPU[pid]; found && interval > 0 && total >= prev {
			cpuPercent = float64(total-prev) / (interval * float64(userHz)) * 100
		}

		// Prefer the fuller cmdline name, fall back to the (truncated) comm.
		procName := comm
		if cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil && len(cmdline) > 0 {
			full := strings.TrimSpace(string(bytes.ReplaceAll(cmdline, []byte{0}, []byte(" "))))
			if full != "" {
				first := full
				if i := strings.IndexByte(full, ' '); i > 0 {
					first = full[:i]
				}
				procName = filepath.Base(first)
			}
		}

		var memMB float64
		if statusData, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
			for _, line := range strings.Split(string(statusData), "\n") {
				if strings.HasPrefix(line, "VmRSS:") {
					var kb int
					fmt.Sscanf(line, "VmRSS: %d", &kb)
					memMB = float64(kb) / 1024
					break
				}
			}
		}

		procs = append(procs, ProcessInfo{PID: pid, Name: procName, CpuPercent: cpuPercent, MemMB: memMB})
	}

	lastProcCPU = curr

	sort.Slice(procs, func(i, j int) bool { return procs[i].CpuPercent > procs[j].CpuPercent })
	if len(procs) > 10 {
		procs = procs[:10]
	}
	return procs
}

// parseStat extracts comm, utime and stime from /proc/[pid]/stat. The comm
// field is wrapped in parentheses and may itself contain spaces or
// parentheses, so we anchor on the last ')' before splitting the rest.
func parseStat(data []byte) (comm string, utime, stime uint64, ok bool) {
	s := string(data)
	open := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if open < 0 || closeIdx < 0 || closeIdx < open {
		return "", 0, 0, false
	}
	comm = s[open+1 : closeIdx]
	rest := strings.Fields(s[closeIdx+1:])
	// rest[0] is field 3 (state); utime is field 14, stime field 15.
	if len(rest) < 13 {
		return "", 0, 0, false
	}
	utime, _ = strconv.ParseUint(rest[11], 10, 64)
	stime, _ = strconv.ParseUint(rest[12], 10, 64)
	return comm, utime, stime, true
}
