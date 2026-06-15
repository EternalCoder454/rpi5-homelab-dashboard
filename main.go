package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"homelab/config"
	"homelab/handlers"
	"homelab/metrics"
)

func main() {
	hashpw := flag.String("hashpw", "", "print a bcrypt hash for the given password and exit (for auth_password_hash)")
	flag.Parse()
	if *hashpw != "" {
		h, err := handlers.HashPassword(*hashpw)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(h)
		return
	}

	// Keep the resident footprint modest on the Pi. SetMemoryLimit gives the GC
	// a soft ceiling and makes the runtime scavenge memory back to the OS;
	// SetGCPercent collects more eagerly so peak heap stays low. Override the
	// limit at runtime with GOMEMLIMIT if you want a different ceiling.
	debug.SetGCPercent(40)
	debug.SetMemoryLimit(256 << 20) // 256 MiB

	if err := config.Load("config.yaml"); err != nil {
		log.Println("Using default config:", err)
	}

	// Ensure the files root exists up front so the file manager and uploads
	// have somewhere to write.
	if err := os.MkdirAll(config.C.UploadDir, 0o755); err != nil {
		log.Println("Could not create upload dir:", err)
	}

	if !handlers.AuthEnabled() {
		log.Println("WARNING: authentication is DISABLED — set auth_user and auth_password_hash in config.yaml")
	}

	// Start the background metrics collectors (2s metrics, 5s processes). All
	// clients read from the shared cache rather than hitting /proc themselves.
	metrics.Start()

	// Start the Docker stats collector (no-op if Docker isn't reachable).
	handlers.StartDocker()

	// Start the hub health-check poller (pings configured targets every 30s).
	handlers.StartHub()

	// Start the network scanner (arp-scan sweep every 2 minutes).
	handlers.StartNetwork()

	// protect gates a data endpoint behind a valid session (no-op when auth is
	// not configured). Static assets are served openly — they hold no data.
	protect := handlers.RequireAuth

	// Auth endpoints (open).
	http.HandleFunc("/api/login", handlers.LoginHandler)
	http.HandleFunc("/api/logout", handlers.LogoutHandler)
	http.HandleFunc("/api/me", handlers.MeHandler)

	http.HandleFunc("/ws/metrics", protect(handlers.MetricsHandler))
	http.HandleFunc("/api/metrics", protect(handlers.MetricsHandler))
	http.HandleFunc("/api/processes", protect(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics.GetProcesses())
	}))
	http.HandleFunc("/ws/terminal", protect(handlers.TerminalHandler))
	http.HandleFunc("/api/upload", protect(handlers.UploadHandler))
	http.HandleFunc("/api/recent-uploads", protect(handlers.UploadHandler))
	http.HandleFunc("/api/exec", protect(handlers.ExecHandler))

	// File manager API (rooted at config.UploadDir).
	http.HandleFunc("/api/files/list", protect(handlers.FilesList))
	http.HandleFunc("/api/files/read", protect(handlers.FilesRead))
	http.HandleFunc("/api/files/write", protect(handlers.FilesWrite))
	http.HandleFunc("/api/files/create", protect(handlers.FilesCreate))
	http.HandleFunc("/api/files/rename", protect(handlers.FilesRename))
	http.HandleFunc("/api/files/delete", protect(handlers.FilesDelete))
	http.HandleFunc("/api/files/download", protect(handlers.FilesDownload))
	http.HandleFunc("/api/files/raw", protect(handlers.FilesRaw))

	// Media: category views + cached thumbnails over the same files root.
	http.HandleFunc("/api/media/scan", protect(handlers.MediaScan))
	http.HandleFunc("/api/media/thumb", protect(handlers.MediaThumb))

	// Filesystem browser: whole-Pi read + edit text, behind a password re-gate.
	http.HandleFunc("/api/fs/status", protect(handlers.FSStatus))
	http.HandleFunc("/api/fs/unlock", protect(handlers.FSUnlock))
	http.HandleFunc("/api/fs/list", protect(handlers.FSList))
	http.HandleFunc("/api/fs/read", protect(handlers.FSRead))
	http.HandleFunc("/api/fs/write", protect(handlers.FSWrite))
	http.HandleFunc("/api/fs/raw", protect(handlers.FSRaw))
	http.HandleFunc("/api/fs/download", protect(handlers.FSDownload))

	// Docker management API.
	http.HandleFunc("/api/docker/containers", protect(handlers.DockerList))
	http.HandleFunc("/api/docker/create", protect(handlers.DockerCreate))
	http.HandleFunc("/api/docker/pull/status", protect(handlers.DockerPullStatus))
	http.HandleFunc("/api/docker/action", protect(handlers.DockerAction))
	http.HandleFunc("/api/docker/top", protect(handlers.DockerTop))
	http.HandleFunc("/api/docker/images", protect(handlers.DockerImages))
	http.HandleFunc("/api/docker/images/remove", protect(handlers.DockerImageRemove))
	http.HandleFunc("/api/docker/volumes", protect(handlers.DockerVolumes))
	http.HandleFunc("/api/docker/volumes/remove", protect(handlers.DockerVolumeRemove))
	http.HandleFunc("/ws/docker/logs", protect(handlers.DockerLogs))
	http.HandleFunc("/ws/docker/terminal", protect(handlers.DockerTerminal))

	// systemd services + host logs.
	http.HandleFunc("/api/services", protect(handlers.ServicesList))
	http.HandleFunc("/api/services/action", protect(handlers.ServicesAction))
	http.HandleFunc("/ws/host/logs", protect(handlers.HostLogs))

	// System controls + updates.
	http.HandleFunc("/api/system/info", protect(handlers.SystemInfo))
	http.HandleFunc("/api/system/power", protect(handlers.SystemPower))
	http.HandleFunc("/api/system/refresh", protect(handlers.SystemRefresh))
	http.HandleFunc("/api/system/upgrade", protect(handlers.SystemUpgrade))
	http.HandleFunc("/api/system/apt-status", protect(handlers.SystemAptStatus))

	// Argon fan control (manual fixed speed / restore automatic curve).
	http.HandleFunc("/api/fan/set", protect(handlers.FanSet))
	http.HandleFunc("/api/fan/auto", protect(handlers.FanAuto))

	// Hub: service links + health checks.
	http.HandleFunc("/api/hub", protect(handlers.HubGet))
	http.HandleFunc("/api/hub/link/add", protect(handlers.HubLinkAdd))
	http.HandleFunc("/api/hub/link/update", protect(handlers.HubLinkUpdate))
	http.HandleFunc("/api/hub/link/remove", protect(handlers.HubRemove))
	http.HandleFunc("/api/hub/check/add", protect(handlers.HubCheckAdd))
	http.HandleFunc("/api/hub/check/update", protect(handlers.HubCheckUpdate))
	http.HandleFunc("/api/hub/check/remove", protect(handlers.HubRemove))

	// Network: LAN device discovery + per-device order/overrides.
	http.HandleFunc("/api/network/devices", protect(handlers.NetworkDevices))
	http.HandleFunc("/api/network/rescan", protect(handlers.NetworkRescan))
	http.HandleFunc("/api/network/order", protect(handlers.NetworkOrder))
	http.HandleFunc("/api/network/device", protect(handlers.NetworkDevice))
	http.HandleFunc("/api/network/stats", protect(handlers.NetworkStats))
	http.HandleFunc("/api/network/pubkey", protect(handlers.NetworkPubkey))

	http.Handle("/", staticHandler("./static"))

	addr := fmt.Sprintf(":%d", config.C.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: gzipMiddleware(http.DefaultServeMux),
		// Header/idle timeouts protect against slow-loris and dangling sockets.
		// Deliberately NO ReadTimeout/WriteTimeout: those would kill the
		// long-lived WebSockets (metrics, terminal, logs).
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if config.C.TLSCert != "" && config.C.TLSKey != "" {
		handlers.SetSecureCookies(true)
		fmt.Printf("Homelab Dashboard (HTTPS) running on %s\n", addr)
		log.Fatal(srv.ListenAndServeTLS(config.C.TLSCert, config.C.TLSKey))
	} else {
		fmt.Printf("Homelab Dashboard (HTTP) running on %s\n", addr)
		log.Fatal(srv.ListenAndServe())
	}
}
