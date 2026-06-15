package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync"
)

var (
	recentUploads []string
	uploadMu      sync.Mutex
)

// UploadHandler handles file uploads (POST /api/upload) and lists recent
// uploads (GET /api/recent-uploads). An optional "dir" form field places the
// file into a subdirectory of the files root (used by the file manager).
func UploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		uploadMu.Lock()
		// Start from an empty (non-nil) slice so an empty history encodes as
		// JSON [] rather than null. null would make the frontend's {#each}
		// throw and wedge Svelte's render loop.
		out := append([]string{}, recentUploads...)
		uploadMu.Unlock()
		if len(out) > 10 {
			out = out[:10]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB
		http.Error(w, "Invalid multipart form", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Strip every directory component from the client-supplied name, then place
	// it inside the (optional) target subdirectory. resolveInRoot guarantees the
	// final path stays within the files root.
	safeName := filepath.Base(filepath.Clean("/" + header.Filename))
	if safeName == "." || safeName == "/" || safeName == "" {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	subdir := r.FormValue("dir")
	dstPath, err := resolveInRoot(path.Join(subdir, safeName))
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		http.Error(w, "Could not save file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Write failed", http.StatusInternalServerError)
		return
	}

	uploadMu.Lock()
	recentUploads = append([]string{dstPath}, recentUploads...)
	if len(recentUploads) > 10 {
		recentUploads = recentUploads[:10]
	}
	uploadMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success", "path": dstPath})
}
