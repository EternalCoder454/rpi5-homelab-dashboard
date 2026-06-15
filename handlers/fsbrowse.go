package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"homelab/config"
)

// A full-filesystem browser: view + edit text files anywhere on the Pi, gated by
// re-entering the login password (a second lock on top of the session). It is
// deliberately limited to read + edit text — no create, rename, or delete — and
// falls back to sudo for paths the service user can't reach directly.

const (
	fsUnlockTTL = 15 * time.Minute
	fsMaxEdit   = 2 << 20 // 2 MiB
)

var (
	fsUnlockMu sync.Mutex
	fsUnlocked = map[string]time.Time{} // session token -> unlock expiry
)

func fsSessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// fsIsUnlocked reports whether this session has passed the filesystem password
// gate. When auth isn't configured the whole dashboard is open, so there is
// nothing to gate with.
func fsIsUnlocked(r *http.Request) bool {
	if !AuthEnabled() {
		return true
	}
	tok := fsSessionToken(r)
	if tok == "" {
		return false
	}
	fsUnlockMu.Lock()
	defer fsUnlockMu.Unlock()
	exp, ok := fsUnlocked[tok]
	if !ok || time.Now().After(exp) {
		delete(fsUnlocked, tok)
		return false
	}
	return true
}

func fsGate(w http.ResponseWriter, r *http.Request) bool {
	if fsIsUnlocked(r) {
		return true
	}
	http.Error(w, "locked", http.StatusForbidden)
	return false
}

// FSStatus — GET /api/fs/status
func FSStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"unlocked": fsIsUnlocked(r), "auth": AuthEnabled()})
}

// FSUnlock — POST /api/fs/unlock {password}
func FSUnlock(w http.ResponseWriter, r *http.Request) {
	if !AuthEnabled() {
		writeJSON(w, map[string]any{"unlocked": true})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(config.C.AuthPasswordHash), []byte(req.Password)) != nil {
		http.Error(w, "Wrong password.", http.StatusUnauthorized)
		return
	}
	tok := fsSessionToken(r)
	if tok == "" {
		http.Error(w, "no session", http.StatusUnauthorized)
		return
	}
	fsUnlockMu.Lock()
	fsUnlocked[tok] = time.Now().Add(fsUnlockTTL)
	fsUnlockMu.Unlock()
	writeJSON(w, map[string]any{"unlocked": true})
}

func cleanAbs(p string) (string, bool) {
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "", false
	}
	return filepath.Clean(p), true
}

// FSList — GET /api/fs/list?path=<abs>
func FSList(w http.ResponseWriter, r *http.Request) {
	if !fsGate(w, r) {
		return
	}
	dir, ok := cleanAbs(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	entries, err := fsReadDir(dir)
	if err != nil {
		http.Error(w, "cannot read directory", http.StatusNotFound)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	writeJSON(w, entries)
}

func fsReadDir(dir string) ([]fileEntry, error) {
	if dirents, err := os.ReadDir(dir); err == nil {
		out := make([]fileEntry, 0, len(dirents))
		for _, d := range dirents {
			info, e := d.Info()
			if e != nil {
				continue
			}
			out = append(out, fileEntry{
				Name: d.Name(), Path: filepath.Join(dir, d.Name()),
				IsDir: d.IsDir(), Size: info.Size(), ModTime: info.ModTime().Unix(),
			})
		}
		return out, nil
	} else if !os.IsPermission(err) {
		return nil, err
	}
	// sudo fallback for root-only directories.
	out, err := exec.Command("sudo", "-n", "find", dir, "-maxdepth", "1", "-mindepth", "1",
		"-printf", "%y\t%s\t%T@\t%f\n").Output()
	if err != nil {
		return nil, err
	}
	var res []fileEntry
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(line, "\t", 4)
		if len(f) != 4 {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		mt, _ := strconv.ParseFloat(f[2], 64)
		res = append(res, fileEntry{
			Name: f[3], Path: filepath.Join(dir, f[3]),
			IsDir: f[0] == "d", Size: size, ModTime: int64(mt),
		})
	}
	return res, nil
}

// FSRead — GET /api/fs/read?path=<abs>
func FSRead(w http.ResponseWriter, r *http.Request) {
	if !fsGate(w, r) {
		return
	}
	p, ok := cleanAbs(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	data, err := fsReadFile(p)
	if err != nil {
		http.Error(w, "cannot read file", http.StatusNotFound)
		return
	}
	if len(data) > fsMaxEdit {
		http.Error(w, "File too large to edit", http.StatusRequestEntityTooLarge)
		return
	}
	if bytes.IndexByte(data, 0) >= 0 {
		http.Error(w, "Binary file", http.StatusUnsupportedMediaType)
		return
	}
	writeJSON(w, map[string]any{"content": string(data), "size": len(data)})
}

// fsReadFile reads up to fsMaxEdit+1 bytes, falling back to sudo for unreadable
// paths.
func fsReadFile(p string) ([]byte, error) {
	if f, err := os.Open(p); err == nil {
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, fsMaxEdit+1))
	} else if !os.IsPermission(err) {
		return nil, err
	}
	return exec.Command("sudo", "-n", "head", "-c", strconv.Itoa(fsMaxEdit+1), "--", p).Output()
}

// FSWrite — POST /api/fs/write {path, content}
func FSWrite(w http.ResponseWriter, r *http.Request) {
	if !fsGate(w, r) {
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p, ok := cleanAbs(req.Path)
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if len(req.Content) > fsMaxEdit {
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := fsWriteFile(p, []byte(req.Content)); err != nil {
		http.Error(w, "Write failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"status": "saved", "size": len(req.Content)})
}

func fsWriteFile(p string, data []byte) error {
	// Atomic direct write (temp in same dir + rename).
	dir := filepath.Dir(p)
	if tmp, err := os.CreateTemp(dir, ".hl-*.tmp"); err == nil {
		name := tmp.Name()
		_, werr := tmp.Write(data)
		if werr == nil {
			werr = tmp.Sync()
		}
		tmp.Close()
		if werr == nil && os.Rename(name, p) == nil {
			return nil
		}
		os.Remove(name)
	}
	// sudo fallback: stage in /tmp, copy over the target (keeps its perms/owner).
	tf, err := os.CreateTemp("/tmp", "hl-fswrite-*")
	if err != nil {
		return err
	}
	tn := tf.Name()
	defer os.Remove(tn)
	tf.Write(data)
	tf.Close()
	out, e := exec.Command("sudo", "-n", "cp", "--", tn, p).CombinedOutput()
	if e != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = e.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// FSRaw — GET /api/fs/raw?path=<abs>  (inline; for previewing images/text)
func FSRaw(w http.ResponseWriter, r *http.Request) {
	if !fsGate(w, r) {
		return
	}
	p, ok := cleanAbs(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		http.Error(w, "not a file", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, p)
}

// FSDownload — GET /api/fs/download?path=<abs>
func FSDownload(w http.ResponseWriter, r *http.Request) {
	if !fsGate(w, r) {
		return
	}
	p, ok := cleanAbs(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		http.Error(w, "not a file", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(p)+"\"")
	http.ServeFile(w, r, p)
}
