package handlers

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"homelab/metrics"

	"golang.org/x/crypto/ssh"
)

// Per-device stats are pulled over SSH using a dedicated key the dashboard
// generates once (ed25519, stored beside hub.json). The user installs the
// matching public key on each machine they want to monitor — no passwords are
// ever stored or transmitted. The Pi running the dashboard is a special case:
// it reuses the metrics we already collect locally, no SSH needed.

const sshKeyFile = "dashboard_ssh_ed25519"

var (
	sshKeyOnce sync.Once
	sshSigner  ssh.Signer
	sshPubLine string
	sshKeyErr  error
)

func ensureSSHKey() (ssh.Signer, string, error) {
	sshKeyOnce.Do(func() {
		if data, err := os.ReadFile(sshKeyFile); err == nil {
			if signer, err := ssh.ParsePrivateKey(data); err == nil {
				sshSigner = signer
				sshPubLine = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
				return
			}
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			sshKeyErr = err
			return
		}
		block, err := ssh.MarshalPrivateKey(priv, "homelab-dashboard")
		if err != nil {
			sshKeyErr = err
			return
		}
		os.WriteFile(sshKeyFile, pem.EncodeToMemory(block), 0o600)
		signer, err := ssh.NewSignerFromKey(priv)
		if err != nil {
			sshKeyErr = err
			return
		}
		sshSigner = signer
		sshPubLine = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	})
	return sshSigner, sshPubLine, sshKeyErr
}

// gpuShell defines a POSIX `gpu` function that resolves a clean GPU model name.
// It reads both the lspci device line and its Subsystem line — the subsystem
// carries the actual board SKU, which disambiguates vendors' combined device
// IDs (e.g. AMD's one "Navi 31" ID shared by 7900 XT / XTX / GRE / 7900M).
const gpuShell = `gpu(){ i=$(lspci -nnk 2>/dev/null | grep -iEA2 'vga compatible|3d controller|display controller' | head -3); d=$(printf '%s' "$i" | head -1); s=$(printf '%s' "$i" | grep -i 'Subsystem:'); m=$(printf '%s\n%s' "$s" "$d" | grep -oE '(RX|RTX|GTX|Arc) [A-Z]?[0-9]{3,5}( ?[A-Z]{1,4})?' | head -1); if [ -n "$m" ]; then case "$m" in RX*) echo "Radeon $m";; RTX*|GTX*) echo "GeForce $m";; *) echo "$m";; esac; else printf '%s' "$d" | cut -d: -f3- | sed -E 's/^ *//; s/ \(rev[^)]*\)//; s/ ?\[[0-9a-f:]+\]//g; s/.*\[([^]]*)\].*/\1/'; fi; }`

// remoteStatCmd is a POSIX one-shot snapshot run on the target over SSH.
const remoteStatCmd = gpuShell + `; echo "HOST $(uname -n)"; echo "OS $(uname -so)"; echo "KERNEL $(uname -r)"; ` +
	`echo "NPROC $(nproc 2>/dev/null || echo 1)"; echo "UPTIME $(cut -d' ' -f1 /proc/uptime)"; ` +
	`echo "LOAD $(cut -d' ' -f1-3 /proc/loadavg)"; ` +
	`awk '/MemTotal|MemAvailable/{gsub(":","",$1); print "MEM " $1 " " $2}' /proc/meminfo; ` +
	`echo "CPU1 $(head -1 /proc/stat)"; sleep 0.3; echo "CPU2 $(head -1 /proc/stat)"; ` +
	`df -kP / | awk 'NR==2{print "DISK " $2 " " $3}'; ` +
	`echo "CPUMODEL $(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | sed 's/^ *//')"; ` +
	`echo "GPU $(gpu)"; ` +
	`echo "DISKMODEL $(lsblk -dno MODEL,SIZE -x SIZE 2>/dev/null | grep -vE '^[[:space:]]*$' | tail -1 | tr -s ' ' | sed 's/^ *//;s/ *$//')"`

func sshStats(ip, user string) (map[string]any, error) {
	signer, _, err := ensureSSHKey()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // LAN homelab; key auth still proves us to the host
		Timeout:         6 * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(ip, "22"), cfg)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	out, err := sess.Output(remoteStatCmd)
	if err != nil {
		return nil, err
	}
	return parseStats(string(out)), nil
}

func parseStats(out string) map[string]any {
	m := map[string]any{}
	var cpu1, cpu2 []uint64
	var memTotal, memAvail float64
	nums := func(fs []string) []uint64 {
		v := make([]uint64, 0, len(fs))
		for _, s := range fs {
			n, _ := strconv.ParseUint(s, 10, 64)
			v = append(v, n)
		}
		return v
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "HOST":
			m["host"] = f[1]
		case "OS":
			m["os"] = strings.Join(f[1:], " ")
		case "KERNEL":
			m["kernel"] = f[1]
		case "NPROC":
			m["cores"], _ = strconv.Atoi(f[1])
		case "UPTIME":
			v, _ := strconv.ParseFloat(f[1], 64)
			m["uptime_seconds"] = int(v)
		case "LOAD":
			m["load"] = strings.Join(f[1:], " ")
		case "MEM":
			if len(f) >= 3 {
				v, _ := strconv.ParseFloat(f[2], 64)
				if f[1] == "MemTotal" {
					memTotal = v
				} else if f[1] == "MemAvailable" {
					memAvail = v
				}
			}
		case "CPU1":
			cpu1 = nums(f[2:])
		case "CPU2":
			cpu2 = nums(f[2:])
		case "DISK":
			if len(f) >= 3 {
				tot, _ := strconv.ParseFloat(f[1], 64)
				used, _ := strconv.ParseFloat(f[2], 64)
				m["disk_total_gb"] = round1(tot / 1048576)
				m["disk_used_gb"] = round1(used / 1048576)
				if tot > 0 {
					m["disk_percent"] = round1(used / tot * 100)
				}
			}
		case "CPUMODEL":
			m["cpu_model"] = strings.Join(f[1:], " ")
		case "GPU":
			m["gpu"] = strings.Join(f[1:], " ")
		case "DISKMODEL":
			m["disk_model"] = strings.Join(f[1:], " ")
		}
	}
	if len(cpu1) >= 5 && len(cpu2) >= 5 {
		var t1, t2 uint64
		for _, v := range cpu1 {
			t1 += v
		}
		for _, v := range cpu2 {
			t2 += v
		}
		i1, i2 := cpu1[3]+cpu1[4], cpu2[3]+cpu2[4]
		if t2 > t1 {
			m["cpu_percent"] = round1(float64((t2-t1)-(i2-i1)) / float64(t2-t1) * 100)
		}
	}
	if memTotal > 0 {
		used := memTotal - memAvail
		m["mem_total_mb"] = int(memTotal / 1024)
		m["mem_used_mb"] = int(used / 1024)
		m["mem_percent"] = round1(used / memTotal * 100)
	}
	return m
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// localHardware reads the Pi's CPU / GPU / disk names once and caches them.
var (
	hwOnce               sync.Once
	hwCPU, hwGPU, hwDisk string
)

func localHardware() (cpu, gpu, disk string) {
	hwOnce.Do(func() {
		if d, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(d), "\n") {
				if strings.HasPrefix(line, "model name") {
					hwCPU = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
					break
				}
			}
		}
		if hwCPU == "" {
			if d, err := os.ReadFile("/proc/device-tree/model"); err == nil {
				hwCPU = strings.TrimRight(string(d), "\x00")
			}
		}
		if out, err := exec.Command("sh", "-c", gpuShell+"; gpu").Output(); err == nil {
			hwGPU = strings.TrimSpace(string(out))
		}
		if out, err := exec.Command("sh", "-c", `lsblk -dno MODEL,SIZE -x SIZE 2>/dev/null | grep -vE '^[[:space:]]*$' | tail -1 | tr -s ' ' | sed 's/^ *//;s/ *$//'`).Output(); err == nil {
			hwDisk = strings.TrimSpace(string(out))
		}
	})
	return hwCPU, hwGPU, hwDisk
}

// localStats formats the dashboard host's own metrics into the SSH stats shape.
func localStats() map[string]any {
	mt := metrics.GetMetrics()
	host, _ := os.Hostname()
	cpu, gpu, disk := localHardware()
	return map[string]any{
		"host":           host,
		"os":             osLabel(),
		"cores":          runtime.NumCPU(),
		"cpu_model":      cpu,
		"gpu":            gpu,
		"disk_model":     disk,
		"cpu_percent":    round1(mt.CpuPercent),
		"mem_total_mb":   mt.MemTotalMB,
		"mem_used_mb":    mt.MemUsedMB,
		"mem_percent":    round1(mt.MemPercent),
		"disk_total_gb":  round1(mt.DiskTotalGB),
		"disk_used_gb":   round1(mt.DiskUsedGB),
		"disk_percent":   round1(mt.DiskPercent),
		"uptime_seconds": mt.UptimeSeconds,
		"load":           fmt.Sprintf("%.2f %.2f %.2f", mt.Load1, mt.Load5, mt.Load15),
	}
}

func sshErrMsg(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "unable to authenticate"):
		return "Key not accepted — install the dashboard's SSH key on this machine, and check the username."
	case strings.Contains(s, "connection refused"):
		return "Connection refused — is an SSH server running on this machine?"
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "Timed out — not reachable on SSH (port 22)."
	}
	return s
}

// isLocalIP reports whether ip belongs to one of this host's interfaces, so the
// dashboard's own Pi resolves to local metrics without needing SSH.
func isLocalIP(ip string) bool {
	target := net.ParseIP(ip)
	if target == nil {
		return false
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(target) {
			return true
		}
	}
	return false
}

// NetworkStats — GET /api/network/stats?ip=&user=
func NetworkStats(w http.ResponseWriter, r *http.Request) {
	ip := strings.TrimSpace(r.URL.Query().Get("ip"))
	if ip == "" {
		http.Error(w, "ip required", http.StatusBadRequest)
		return
	}
	if isLocalIP(ip) {
		writeJSON(w, map[string]any{"ok": true, "self": true, "stats": localStats()})
		return
	}
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	if user == "" {
		http.Error(w, "user required", http.StatusBadRequest)
		return
	}
	stats, err := sshStats(ip, user)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": sshErrMsg(err)})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "stats": stats})
}

// NetworkPubkey — GET /api/network/pubkey (the key to install on target hosts)
func NetworkPubkey(w http.ResponseWriter, r *http.Request) {
	_, pub, err := ensureSSHKey()
	if err != nil {
		http.Error(w, "key error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"pubkey": pub})
}
