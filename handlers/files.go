package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"homelab/config"
)

// The file manager is rooted at config.UploadDir. Every request path is
// resolved through resolveInRoot so it can never escape that directory — this
// endpoint is deliberately scoped (unlike the terminal/exec which are full
// shell access).

const maxEditBytes = 2 << 20 // 2 MiB: refuse to load anything larger into the editor

func filesRoot() string { return config.C.UploadDir }

// resolveInRoot maps a client-supplied relative path to an absolute path that
// is guaranteed to live inside the files root. filepath.Clean("/"+rel)
// collapses any ".." segments before the join, so traversal is impossible.
func resolveInRoot(rel string) (string, error) {
	root, err := filepath.Abs(filesRoot())
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.Clean("/"+rel))
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", errors.New("path escapes root")
	}
	return full, nil
}

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"` // relative to root, forward-slashed
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"` // unix seconds
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// FilesList — GET /api/files/list?path=<rel>
func FilesList(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	full, err := resolveInRoot(rel)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filesRoot(), 0o755); err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	dirents, err := os.ReadDir(full)
	if err != nil {
		http.Error(w, "Cannot read directory", http.StatusNotFound)
		return
	}
	out := make([]fileEntry, 0, len(dirents))
	for _, d := range dirents {
		info, err := d.Info()
		if err != nil {
			continue
		}
		out = append(out, fileEntry{
			Name:    d.Name(),
			Path:    path.Join(rel, d.Name()),
			IsDir:   d.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
		})
	}
	// Directories first, then case-insensitive name order.
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, out)
}

// FilesRead — GET /api/files/read?path=<rel>
func FilesRead(w http.ResponseWriter, r *http.Request) {
	full, err := resolveInRoot(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "Not a file", http.StatusNotFound)
		return
	}
	if info.Size() > maxEditBytes {
		http.Error(w, "File too large to edit", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		http.Error(w, "Cannot read file", http.StatusInternalServerError)
		return
	}
	if bytes.IndexByte(data, 0) >= 0 {
		http.Error(w, "Binary file", http.StatusUnsupportedMediaType)
		return
	}
	writeJSON(w, map[string]any{"content": string(data), "size": info.Size()})
}

// FilesWrite — POST /api/files/write {path, content}. Atomic: writes a temp
// file in the same directory, fsyncs it, then renames over the target so a
// power loss can never leave a half-written file (important on SD cards).
func FilesWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	full, err := resolveInRoot(req.Path)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if len(req.Content) > maxEditBytes {
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}

	dir := filepath.Dir(full)
	tmp, err := os.CreateTemp(dir, ".hl-*.tmp")
	if err != nil {
		http.Error(w, "Cannot write", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(req.Content)
	if werr == nil {
		werr = tmp.Sync() // flush to disk before the rename
	}
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmpName)
		http.Error(w, "Write failed", http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmpName, full); err != nil {
		os.Remove(tmpName)
		http.Error(w, "Rename failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "saved", "size": len(req.Content)})
}

// FilesCreate — POST /api/files/create {path, dir}
func FilesCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Dir  bool   `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	full, err := resolveInRoot(req.Path)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(full); err == nil {
		http.Error(w, "Already exists", http.StatusConflict)
		return
	}
	if req.Dir {
		if err := os.MkdirAll(full, 0o755); err != nil {
			http.Error(w, "Cannot create folder", http.StatusInternalServerError)
			return
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			http.Error(w, "Cannot create file", http.StatusInternalServerError)
			return
		}
		f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			http.Error(w, "Cannot create file", http.StatusInternalServerError)
			return
		}
		f.Close()
	}
	writeJSON(w, map[string]any{"status": "created"})
}

// FilesRename — POST /api/files/rename {from, to}
func FilesRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	from, e1 := resolveInRoot(req.From)
	to, e2 := resolveInRoot(req.To)
	if e1 != nil || e2 != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(to); err == nil {
		http.Error(w, "Target already exists", http.StatusConflict)
		return
	}
	if err := os.Rename(from, to); err != nil {
		http.Error(w, "Rename failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "renamed"})
}

// FilesDelete — POST /api/files/delete {path}
func FilesDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	full, err := resolveInRoot(req.Path)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	root, _ := filepath.Abs(filesRoot())
	if full == root {
		http.Error(w, "Refusing to delete root", http.StatusBadRequest)
		return
	}
	if err := os.RemoveAll(full); err != nil {
		http.Error(w, "Delete failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "deleted"})
}

// FilesRaw — GET /api/files/raw?path=<rel>
// Serves the file inline (no attachment header) so the browser can render it
// in an <img>/<video>/<audio> tag. http.ServeFile sets the Content-Type from
// the extension and supports HTTP Range requests, so video seeking works.
func FilesRaw(w http.ResponseWriter, r *http.Request) {
	full, err := resolveInRoot(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "Not a file", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, full)
}

// FilesDownload — GET /api/files/download?path=<rel>
func FilesDownload(w http.ResponseWriter, r *http.Request) {
	full, err := resolveInRoot(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "Not a file", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(full)+"\"")
	http.ServeFile(w, r, full)
}
