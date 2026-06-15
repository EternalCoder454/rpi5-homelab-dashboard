package handlers

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Network tab discovers LAN devices with an arp-scan sweep, classifies each
// as computer / printer / router / other (open ports first, vendor as a
// fallback), and lets the user reorder, rename, and hide them. The custom order
// and per-device overrides persist to network.json (keyed by MAC, the only
// stable identifier across DHCP leases).

const networkFile = "network.json"

// deviceMeta is the user's persisted per-device settings.
type deviceMeta struct {
	MAC    string   `json:"mac"`
	Name   string   `json:"name,omitempty"`
	Hidden bool     `json:"hidden,omitempty"`
	Tags   []string `json:"tags,omitempty"` // up to 3 user labels
	User   string   `json:"user,omitempty"` // saved SSH username for live stats
}

// defaultTag seeds a tag from the auto-classification when the user hasn't set
// their own.
func defaultTag(kind string) string {
	switch kind {
	case "computer":
		return "Computer"
	case "router":
		return "Router"
	case "printer":
		return "Printer"
	}
	return ""
}

type networkStore struct {
	Order []string     `json:"order"` // MACs, in display order
	Meta  []deviceMeta `json:"meta"`
}

// device is a discovered + classified host (the live view sent to the client).
type device struct {
	MAC       string   `json:"mac"`
	IP        string   `json:"ip"`
	Vendor    string   `json:"vendor"`
	Hostname  string   `json:"hostname"`
	Kind      string   `json:"kind"` // computer | printer | router | other
	Online    bool     `json:"online"`
	OpenPorts []int    `json:"open_ports"`
	Self      bool     `json:"self"` // the Pi running this dashboard
	Name      string   `json:"name,omitempty"`
	Hidden    bool     `json:"hidden"`
	Tags      []string `json:"tags"`
	User      string   `json:"user,omitempty"` // saved SSH username (set = "connected")
}

var (
	netMu       sync.RWMutex
	netCache    []device
	netScanAt   time.Time
	netScanning bool

	netLastReq time.Time // last time a client asked for the device list

	netStoreMu sync.Mutex
	netCfg     networkStore
	netOnce    sync.Once
)

// StartNetwork loads the saved order/overrides and begins a background scan
// loop. The first scan runs immediately; subsequent ones every 2 minutes.
func StartNetwork() {
	netOnce.Do(func() {
		if data, err := os.ReadFile(networkFile); err == nil {
			netStoreMu.Lock()
			json.Unmarshal(data, &netCfg)
			netStoreMu.Unlock()
		}
		go func() {
			runScan() // warm the cache once at boot
			t := time.NewTicker(2 * time.Minute)
			defer t.Stop()
			for range t.C {
				// Skip the periodic sweep when nobody has looked at the Network
				// tab recently — no point hammering the LAN with arp-scan while
				// idle. Viewing the tab triggers an immediate refresh below.
				netMu.RLock()
				idle := netLastReq.IsZero() || time.Since(netLastReq) > 5*time.Minute
				netMu.RUnlock()
				if !idle {
					runScan()
				}
			}
		}()
	})
}

func runScan() {
	netMu.Lock()
	if netScanning {
		netMu.Unlock()
		return
	}
	netScanning = true
	netMu.Unlock()

	devs := scanNetwork()

	netMu.Lock()
	netCache = devs
	netScanAt = time.Now()
	netScanning = false
	netMu.Unlock()
}

// --- discovery + classification --------------------------------------------

type arpEntry struct{ IP, MAC, Vendor string }

var arpLine = regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+)\s+([0-9a-fA-F:]{17})\s*(.*)$`)

func scanNetwork() []device {
	gw, iface := defaultRoute()
	entries := arpScan(iface)

	devs := make([]device, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Add(1)
		go func(i int, e arpEntry) {
			defer wg.Done()
			ports := probePorts(e.IP)
			vendor := vendorForMAC(e.MAC)
			if vendor == "" {
				vendor = "Unknown"
			}
			devs[i] = device{
				MAC:       e.MAC,
				IP:        e.IP,
				Vendor:    vendor,
				Hostname:  lookupHostname(e.IP),
				OpenPorts: ports,
				Online:    true,
				Kind:      classify(e.IP, vendor, ports, gw),
			}
		}(i, e)
	}
	wg.Wait()

	if self, ok := selfDevice(iface); ok {
		devs = append(devs, self)
	}
	return devs
}

// arpScan runs an arp-scan sweep of the local subnet and parses IP/MAC/vendor.
func arpScan(iface string) []arpEntry {
	out, err := exec.Command("sudo", "-n", "arp-scan", "-I", iface, "--localnet", "--retry=2").Output()
	if err != nil {
		return nil
	}
	var res []arpEntry
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		m := arpLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		mac := strings.ToLower(m[2])
		if seen[mac] {
			continue
		}
		seen[mac] = true
		res = append(res, arpEntry{IP: m[1], MAC: mac, Vendor: strings.TrimSpace(m[3])})
	}
	return res
}

// --- MAC vendor lookup ------------------------------------------------------
//
// arp-scan ships an OUI database but drops privileges before reading it, so it
// reports "(Unknown)". We read the same file ourselves (it's world-readable) and
// resolve vendors directly — more reliable, and it sharpens classification.

var (
	ouiMap  map[string]string
	ouiOnce sync.Once
)

func loadOUI() {
	ouiOnce.Do(func() {
		ouiMap = map[string]string{}
		data, err := os.ReadFile("/usr/share/arp-scan/ieee-oui.txt")
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" || line[0] == '#' {
				continue
			}
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			ouiMap[strings.ToUpper(strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	})
}

func vendorForMAC(mac string) string {
	h := strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if len(h) < 6 {
		return ""
	}
	// Locally-administered (randomized) MACs have bit 0x02 set in the first
	// octet and no real vendor.
	if b, err := strconv.ParseUint(h[0:2], 16, 16); err == nil && b&0x02 != 0 {
		return "Randomized MAC"
	}
	loadOUI()
	for _, n := range []int{9, 7, 6} { // MA-S / MA-M / MA-L assignment sizes
		if len(h) >= n {
			if v, ok := ouiMap[h[:n]]; ok {
				return v
			}
		}
	}
	return ""
}

// probePorts TCP-connects to a handful of telltale ports concurrently.
func probePorts(ip string) []int {
	ports := []int{22, 139, 445, 3389, 5900, 80, 443, 9100, 631, 515}
	var mu sync.Mutex
	var wg sync.WaitGroup
	open := []int{}
	for _, p := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(p)), 600*time.Millisecond)
			if err == nil {
				c.Close()
				mu.Lock()
				open = append(open, p)
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	sort.Ints(open)
	return open
}

func classify(ip, vendor string, ports []int, gateway string) string {
	if ip == gateway {
		return "router"
	}
	has := func(p int) bool {
		for _, x := range ports {
			if x == p {
				return true
			}
		}
		return false
	}
	// Open ports are the most reliable signal.
	if has(22) || has(3389) || has(5900) || has(445) || has(139) {
		return "computer"
	}
	if has(9100) || has(631) || has(515) {
		return "printer"
	}
	// Fall back to the MAC vendor. HP is deliberately omitted — it makes both
	// PCs and printers, so its devices are classified by ports above.
	v := strings.ToLower(vendor)
	switch {
	case containsAny(v, "apple", "dell", "asus", "micro-star", "gigabyte", "intel", "lenovo", "framework", "supermicro", "razer", "valve"):
		return "computer"
	case containsAny(v, "canon", "brother", "epson", "lexmark", "xerox", "kyocera", "ricoh"):
		return "printer"
	case containsAny(v, "technicolor", "netgear", "tp-link", "ubiquiti", "cisco", "mikrotik", "arris", "eero", "fortinet"):
		return "router"
	}
	return "other"
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func lookupHostname(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// defaultRoute returns the default gateway IP and the interface it uses.
func defaultRoute() (gateway, iface string) {
	iface = "wlan0"
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return "", iface
	}
	f := strings.Fields(string(out))
	for i := 0; i+1 < len(f); i++ {
		switch f[i] {
		case "via":
			gateway = f[i+1]
		case "dev":
			iface = f[i+1]
		}
	}
	return gateway, iface
}

// selfDevice describes the Pi running the dashboard — arp-scan can't see its
// own host, so add it explicitly.
func selfDevice(iface string) (device, bool) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return device{}, false
	}
	addrs, _ := ifi.Addrs()
	ip := ""
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			ip = ipn.IP.String()
			break
		}
	}
	if ip == "" {
		return device{}, false
	}
	host, _ := os.Hostname()
	return device{
		MAC:       strings.ToLower(ifi.HardwareAddr.String()),
		IP:        ip,
		Vendor:    "This device",
		Hostname:  host,
		Kind:      "computer",
		Online:    true,
		Self:      true,
		OpenPorts: probePorts(ip),
	}, true
}

// --- persistence ------------------------------------------------------------

func saveNetStoreLocked() error {
	data, _ := json.MarshalIndent(netCfg, "", "  ")
	tmp, err := os.CreateTemp(".", ".network-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	tmp.Write(data)
	tmp.Sync()
	tmp.Close()
	return os.Rename(name, networkFile)
}

func ipLess(a, b string) bool {
	pa, pb := net.ParseIP(a).To4(), net.ParseIP(b).To4()
	if pa == nil || pb == nil {
		return a < b
	}
	for i := 0; i < 4; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

// --- handlers ---------------------------------------------------------------

// orderedDevices merges the live scan with saved order + per-device overrides.
func orderedDevices() ([]device, time.Time, bool) {
	netMu.RLock()
	devs := append([]device(nil), netCache...)
	at, scanning := netScanAt, netScanning
	netMu.RUnlock()

	netStoreMu.Lock()
	byMAC := map[string]deviceMeta{}
	for _, m := range netCfg.Meta {
		byMAC[m.MAC] = m
	}
	pos := map[string]int{}
	for i, mac := range netCfg.Order {
		pos[mac] = i
	}
	netStoreMu.Unlock()

	for i := range devs {
		if m, ok := byMAC[devs[i].MAC]; ok {
			devs[i].Name = m.Name
			devs[i].Hidden = m.Hidden
			devs[i].User = m.User
			devs[i].Tags = m.Tags
		}
		// Seed a tag from auto-classification when the user hasn't set any.
		if len(devs[i].Tags) == 0 {
			if t := defaultTag(devs[i].Kind); t != "" {
				devs[i].Tags = []string{t}
			}
		}
		if devs[i].Tags == nil {
			devs[i].Tags = []string{}
		}
	}
	sort.SliceStable(devs, func(i, j int) bool {
		pi, oki := pos[devs[i].MAC]
		pj, okj := pos[devs[j].MAC]
		if oki && okj {
			return pi < pj
		}
		if oki != okj {
			return oki // explicitly-ordered devices come first
		}
		return ipLess(devs[i].IP, devs[j].IP)
	})
	return devs, at, scanning
}

// NetworkDevices — GET /api/network/devices
func NetworkDevices(w http.ResponseWriter, r *http.Request) {
	devs, at, scanning := orderedDevices()
	// Record interest and kick off a fresh scan if the cache has gone stale, so
	// reopening the tab after idle shows current data within a poll cycle.
	netMu.Lock()
	netLastReq = time.Now()
	stale := !scanning && time.Since(netScanAt) > 2*time.Minute
	netMu.Unlock()
	if stale {
		go runScan()
		scanning = true
	}
	writeJSON(w, map[string]any{"devices": devs, "scanned_at": at.Unix(), "scanning": scanning})
}

// NetworkRescan — POST /api/network/rescan (runs a fresh scan synchronously)
func NetworkRescan(w http.ResponseWriter, r *http.Request) {
	runScan()
	devs, at, scanning := orderedDevices()
	writeJSON(w, map[string]any{"devices": devs, "scanned_at": at.Unix(), "scanning": scanning})
}

// NetworkOrder — POST /api/network/order {order: [mac, ...]}
func NetworkOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	netStoreMu.Lock()
	netCfg.Order = in.Order
	err := saveNetStoreLocked()
	netStoreMu.Unlock()
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}

// NetworkDevice — POST /api/network/device {mac, name?, hidden?, force?}
func NetworkDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceMeta
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.MAC) == "" {
		http.Error(w, "mac required", http.StatusBadRequest)
		return
	}
	in.MAC = strings.ToLower(in.MAC)
	if len(in.Tags) > 3 {
		in.Tags = in.Tags[:3]
	}
	netStoreMu.Lock()
	found := false
	for i := range netCfg.Meta {
		if netCfg.Meta[i].MAC == in.MAC {
			netCfg.Meta[i] = in
			found = true
			break
		}
	}
	if !found {
		netCfg.Meta = append(netCfg.Meta, in)
	}
	err := saveNetStoreLocked()
	netStoreMu.Unlock()
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "ok"})
}
