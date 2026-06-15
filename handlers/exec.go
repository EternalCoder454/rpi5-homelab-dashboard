package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"os/exec"

	"homelab/config"
)

// allowList holds the parsed CIDR/IP allowlist for the exec endpoint.
var allowList []netip.Prefix

func init() {
	for _, entry := range config.C.ExecAllowlist {
		// Accept both CIDR notation ("192.168.1.0/24") and bare addresses
		// ("127.0.0.1"), normalizing the latter to a host-sized prefix.
		if p, err := netip.ParsePrefix(entry); err == nil {
			allowList = append(allowList, p.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			allowList = append(allowList, netip.PrefixFrom(addr, addr.BitLen()))
		}
	}
}

// clientAllowed reports whether the request's remote address is permitted to
// use the exec endpoint. Uses net.SplitHostPort so IPv6 (e.g. "[::1]:1234")
// is parsed correctly.
func clientAllowed(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // may already be a bare IP
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return true
	}
	for _, prefix := range allowList {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// ExecHandler runs a shell command supplied by an allowlisted client.
//
// SECURITY: this endpoint is arbitrary remote command execution gated only by
// a source-IP allowlist. It is unauthenticated by design; do not expose it
// beyond a trusted LAN without adding authentication (see follow-ups).
func ExecHandler(w http.ResponseWriter, r *http.Request) {
	if !clientAllowed(r.RemoteAddr) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	cmd := exec.Command("sh", "-c", req.Command)
	out, err := cmd.CombinedOutput()
	resp := map[string]string{"stdout": string(out)}
	if err != nil {
		resp["stderr"] = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
