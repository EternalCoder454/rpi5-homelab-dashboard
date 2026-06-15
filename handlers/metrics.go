package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"homelab/metrics"
)

// upgrader is shared by every WebSocket handler in this package.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// writeWait bounds a single WebSocket write so one slow/stalled client can
// never block the ticker indefinitely.
const writeWait = 5 * time.Second

// MetricsHandler serves both the one-shot JSON snapshot (/api/metrics) and the
// streaming WebSocket feed (/ws/metrics). Both read from the shared metrics
// cache, so connecting N clients does not multiply /proc reads.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/metrics" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics.GetMetrics())
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	send := func() bool {
		data, err := json.Marshal(metrics.GetMetrics())
		if err != nil {
			return true // skip this frame, keep the connection
		}
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.TextMessage, data) == nil
	}

	// Push one frame immediately from the warm cache so the dashboard paints
	// real numbers within an RTT instead of waiting up to a full tick.
	if !send() {
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if !send() {
			return
		}
	}
}
