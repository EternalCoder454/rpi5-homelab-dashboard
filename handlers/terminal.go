package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"homelab/config"
)

// TerminalHandler bridges a browser xterm.js session to a real PTY-backed
// shell over a WebSocket.
//
// Message protocol (driven by the frontend, do not change here):
//   - input:  {"type":"input","data":"<bytes>"}  -> written to the PTY
//   - resize: {"rows":<n>,"cols":<n>}             -> pty.Setsize
//
// PTY output is streamed back to the browser as binary frames.
func TerminalHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	cmd := exec.Command(config.C.Shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return
	}
	// NOTE: ptmx is intentionally NOT closed via defer here. The PTY->WS reader
	// goroutine below blocks on ptmx.Read until the PTY is closed. If we
	// deferred the close to the outer function it could only run after
	// wg.Wait() returns, but wg.Wait() is itself waiting on that reader — a
	// deadlock. Instead we close ptmx explicitly once the client's read loop
	// ends, which unblocks the reader and lets wg.Wait() return cleanly.

	go func() { _ = cmd.Wait() }()

	var wg sync.WaitGroup
	wg.Add(1)

	// PTY -> WS: stream shell output to the browser as binary frames.
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
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

	// WS -> PTY: runs in this goroutine and returns when the client disconnects.
	for {
		msgType, p, err := conn.ReadMessage()
		if err != nil {
			break
		}

		switch msgType {
		case websocket.TextMessage:
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
				_, _ = ptmx.Write([]byte(msg.Data))
			case msg.Type == "resize" || msg.Rows > 0:
				_ = pty.Setsize(ptmx, &pty.Winsize{
					Rows: uint16(msg.Rows),
					Cols: uint16(msg.Cols),
				})
			}
		case websocket.BinaryMessage:
			// Fallback: treat raw binary frames as direct PTY input.
			_, _ = ptmx.Write(p)
		}
	}

	// Client disconnected (or read error). Close the PTY to unblock the reader
	// goroutine and kill the shell, then wait for the reader to finish.
	_ = ptmx.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	wg.Wait()
}
