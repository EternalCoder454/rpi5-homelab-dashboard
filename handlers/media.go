package handlers

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Media adds category views (gallery / videos / documents) over the same
// sandboxed files root, with on-demand thumbnails cached to disk. Everything is
// kept light for the Pi: thumbnails are generated once and reused, generation is
// capped to a couple at a time, and the recursive scan is bounded.

const thumbMax = 256 // px on the long edge

// thumbSem caps how many thumbnails generate concurrently so a folder of photos
// can't spike CPU/memory.
var thumbSem = make(chan struct{}, 2)

func thumbCacheDir() string { return filepath.Join(filepath.Dir(filesRoot()), ".thumbcache") }

func extOf(name string) string { return strings.ToLower(filepath.Ext(name)) }

var (
	imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true, ".svg": true, ".heic": true, ".heif": true}
	videoExts = map[string]bool{".mp4": true, ".mkv": true, ".mov": true, ".webm": true, ".avi": true, ".m4v": true}
	docExts   = map[string]bool{".txt": true, ".md": true, ".markdown": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true, ".cfg": true, ".log": true, ".csv": true, ".xml": true, ".html": true, ".css": true, ".js": true, ".ts": true, ".sh": true, ".py": true, ".go": true, ".rs": true, ".c": true, ".h": true, ".env": true}
)

func catOf(name string) string {
	e := extOf(name)
	switch {
	case imageExts[e]:
		return "image"
	case videoExts[e]:
		return "video"
	case docExts[e]:
		return "doc"
	}
	return "other"
}

var (
	ffmpegOnce sync.Once
	ffmpegPath string
)

func ffmpegBin() string {
	ffmpegOnce.Do(func() { ffmpegPath, _ = exec.LookPath("ffmpeg") })
	return ffmpegPath
}

// thumbable reports whether we can render a real thumbnail (Go-decodable image,
// or video when ffmpeg is present). SVGs render directly; HEIC can't be decoded
// in pure Go, so those fall back to an icon on the client.
func thumbable(name string) bool {
	e := extOf(name)
	if videoExts[e] {
		return ffmpegBin() != ""
	}
	switch e {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// MediaScan — GET /api/media/scan?cat=gallery|videos|documents
// Recursively collects matching files under the media root (bounded), newest
// first, each tagged with whether a thumbnail is available.
func MediaScan(w http.ResponseWriter, r *http.Request) {
	var match func(string) bool
	switch r.URL.Query().Get("cat") {
	case "gallery":
		match = func(n string) bool { return catOf(n) == "image" }
	case "videos":
		match = func(n string) bool { return catOf(n) == "video" }
	case "documents":
		match = func(n string) bool { return catOf(n) == "doc" }
	default:
		http.Error(w, "bad cat", http.StatusBadRequest)
		return
	}

	root, _ := filepath.Abs(filesRoot())
	os.MkdirAll(root, 0o755)
	const maxFiles = 2000
	type mediaEntry struct {
		fileEntry
		Cat   string `json:"cat"`
		Thumb bool   `json:"thumb"`
	}
	out := make([]mediaEntry, 0, 64)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= maxFiles {
			return filepath.SkipAll
		}
		if !match(d.Name()) {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, mediaEntry{
			fileEntry: fileEntry{Name: d.Name(), Path: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime().Unix()},
			Cat:       catOf(d.Name()),
			Thumb:     thumbable(d.Name()),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	writeJSON(w, out)
}

// MediaThumb — GET /api/media/thumb?path=<rel>. Serves a cached ~256px JPEG,
// generating + caching it on first request.
func MediaThumb(w http.ResponseWriter, r *http.Request) {
	full, err := resolveInRoot(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.Error(w, "not a file", http.StatusNotFound)
		return
	}
	if !thumbable(filepath.Base(full)) {
		http.Error(w, "no thumbnail", http.StatusUnsupportedMediaType)
		return
	}

	key := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%d", full, info.ModTime().UnixNano(), info.Size())))
	cacheFile := filepath.Join(thumbCacheDir(), hex.EncodeToString(key[:])+".jpg")

	serve := func() {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, cacheFile)
	}
	if _, err := os.Stat(cacheFile); err == nil {
		serve()
		return
	}

	thumbSem <- struct{}{}
	defer func() { <-thumbSem }()
	if _, err := os.Stat(cacheFile); err == nil { // another request may have built it while we waited
		serve()
		return
	}
	os.MkdirAll(thumbCacheDir(), 0o755)

	var gerr error
	if videoExts[extOf(full)] {
		gerr = genVideoThumb(full, cacheFile)
	} else {
		gerr = genImageThumb(full, cacheFile)
	}
	if gerr != nil {
		http.Error(w, "thumbnail failed", http.StatusInternalServerError)
		return
	}
	serve()
}

func genImageThumb(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := img.Bounds()
	nw, nh := scaleDims(b.Dx(), b.Dy(), thumbMax)
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(out, out.Bounds(), img, b, draw.Over, nil)
	return encodeJPEG(out, dst)
}

func genVideoThumb(src, dst string) error {
	bin := ffmpegBin()
	if bin == "" {
		return fmt.Errorf("no ffmpeg")
	}
	tmp := dst + ".tmp.jpg"
	cmd := exec.Command(bin, "-y", "-ss", "1", "-i", src, "-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:-2", thumbMax), tmp)
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func encodeJPEG(img image.Image, dst string) error {
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, dst)
}

func scaleDims(w, h, max int) (int, int) {
	if w <= 0 || h <= 0 {
		return max, max
	}
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		return max, h * max / w
	}
	return w * max / h, max
}
