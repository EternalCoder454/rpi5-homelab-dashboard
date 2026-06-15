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
	DiskName      string  `json:"disk_name"`
	DiskTempC     float64 `json:"disk_temp_c"`
	DiskReadMBps  float64 `json:"disk_read_mbps"`
	DiskWriteMBps float64 `json:"disk_write_mbps"`
	NetRxKbps     float64 `json:"net_rx_kbps"`
	NetTxKbps     float64 `json:"net_tx_kbps"`
	Load1         float64 `json:"load_1"`
	Load5         float64 `json:"load_5"`
	Load15        float64 `json:"load_15"`
	UptimeSeconds int     `json:"uptime_seconds"`
	FanState      int     `json:"fan_state"`
	FanPercent    int     `json:"fan_percent"`
	FanMode       string  `json:"fan_mode"`
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
	readDiskIO(m)
	readDiskTemp(m)
	readNetDev(m)
	readLoadAvg(m)
	readFanState(m)
	readFan(m)
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
	m.DiskName = primaryDiskName()
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

// The disk backing "/" never changes at runtime, so resolve its device node
// (e.g. "nvme0n1") and a clean human label once and cache both.
var (
	diskOnce sync.Once
	diskNode string
	diskName string
)

func diskInit() {
	diskOnce.Do(func() {
		diskNode = parentDisk(rootDevice())
		if model := diskModel(diskNode); model != "" {
			diskName = simplifyDiskName(model)
			return
		}
		switch {
		case strings.HasPrefix(diskNode, "nvme"):
			diskName = "NVMe SSD"
		case strings.HasPrefix(diskNode, "mmcblk"):
			diskName = "SD card"
		default:
			diskName = diskNode
		}
	})
}

func primaryDiskName() string { diskInit(); return diskName }
func primaryDiskNode() string { diskInit(); return diskNode }

// readDiskIO reports read/write throughput (MB/s) for the primary disk from
// /proc/diskstats sector deltas. Delta state is owned by the metrics goroutine.
var (
	lastDiskRd   uint64
	lastDiskWr   uint64
	lastDiskTime time.Time
)

func readDiskIO(m *SystemMetrics) {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return
	}
	node := primaryDiskNode()
	var rd, wr uint64
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[2] != node {
			continue
		}
		rd, _ = strconv.ParseUint(f[5], 10, 64) // sectors read
		wr, _ = strconv.ParseUint(f[9], 10, 64) // sectors written
		found = true
		break
	}
	if !found {
		return
	}
	now := time.Now()
	interval := now.Sub(lastDiskTime).Seconds()
	if !lastDiskTime.IsZero() && interval > 0 {
		if rd >= lastDiskRd {
			m.DiskReadMBps = float64(rd-lastDiskRd) * 512 / 1e6 / interval
		}
		if wr >= lastDiskWr {
			m.DiskWriteMBps = float64(wr-lastDiskWr) * 512 / 1e6 / interval
		}
	}
	lastDiskRd, lastDiskWr, lastDiskTime = rd, wr, now
}

// readDiskTemp reads the primary disk's onboard thermal sensor (NVMe drives
// expose one via hwmon). Left at 0 for media without a sensor (e.g. SD cards).
func readDiskTemp(m *SystemMetrics) {
	node := primaryDiskNode()
	matches, _ := filepath.Glob("/sys/block/" + node + "/device/hwmon*/temp1_input")
	if len(matches) == 0 {
		matches, _ = filepath.Glob("/sys/block/" + node + "/device/hwmon/hwmon*/temp1_input")
	}
	for _, p := range matches {
		if b, err := os.ReadFile(p); err == nil {
			if v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil && v > 0 {
				m.DiskTempC = v / 1000.0
				return
			}
		}
	}
}

// readFan reports the Argon fan duty (%) that argononed applies for the current
// CPU temperature, replicating its curve from /etc/argononed.conf (highest
// threshold <= temp wins; below all thresholds is 0%). FanPercent is -1 when no
// Argon config is present, so the UI can fall back to the kernel fan state.
func readFan(m *SystemMetrics) {
	m.FanPercent = -1
	data, err := os.ReadFile("/etc/argononed.conf")
	if err != nil {
		return
	}
	if strings.Contains(string(data), "dashboard-manual") {
		m.FanMode = "manual"
	} else {
		m.FanMode = "auto"
	}
	type point struct {
		temp  float64
		speed int
	}
	var curve []point
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		t, e1 := strconv.ParseFloat(strings.TrimSpace(kv[0]), 64)
		s, e2 := strconv.Atoi(strings.TrimSpace(kv[1]))
		if e1 == nil && e2 == nil {
			curve = append(curve, point{t, s})
		}
	}
	if len(curve) == 0 {
		return
	}
	sort.Slice(curve, func(i, j int) bool { return curve[i].temp > curve[j].temp })
	speed := 0
	for _, p := range curve {
		if m.CpuTempC >= p.temp {
			speed = p.speed
			break
		}
	}
	m.FanPercent = speed
}

// rootDevice returns the device backing "/" (e.g. "nvme0n1p2"), sans /dev/.
func rootDevice() string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "/" {
			return strings.TrimPrefix(f[0], "/dev/")
		}
	}
	return ""
}

// parentDisk maps a partition to its whole-disk device via sysfs, so it handles
// nvme0n1p2->nvme0n1, mmcblk0p2->mmcblk0 and sda2->sda alike.
func parentDisk(dev string) string {
	if dev == "" {
		return ""
	}
	if _, err := os.Stat("/sys/class/block/" + dev + "/partition"); err != nil {
		return dev // already a whole disk (or unknown)
	}
	link, err := filepath.EvalSymlinks("/sys/class/block/" + dev)
	if err != nil {
		return dev
	}
	return filepath.Base(filepath.Dir(link))
}

func diskModel(disk string) string {
	if disk == "" {
		return ""
	}
	for _, p := range []string{
		"/sys/block/" + disk + "/device/model", // nvme, sata, usb
		"/sys/block/" + disk + "/device/name",  // mmc/sd
	} {
		if b, err := os.ReadFile(p); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	return ""
}

// simplifyDiskName collapses whitespace and drops a trailing part-number token,
// e.g. "SK hynix BC711 HFM256GD3JX013N" -> "SK hynix BC711".
func simplifyDiskName(model string) string {
	fields := strings.Fields(model)
	if len(fields) > 2 {
		last := fields[len(fields)-1]
		if len(last) >= 10 && strings.ContainsAny(last, "0123456789") {
			fields = fields[:len(fields)-1]
		}
	}
	return strings.Join(fields, " ")
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
