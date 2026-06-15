package main

import (
	"compress/gzip"
	"net/http"
	"strings"
)

// compressibleType reports whether a Content-Type is worth gzipping. Binary
// assets (fonts/images/video) are already compressed, so we skip them.
func compressibleType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "application/json") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "image/svg") ||
		strings.Contains(ct, "application/xml") ||
		strings.Contains(ct, "application/wasm")
}

// binaryExt paths are served straight through (no gzip wrapper) so the
// FileServer can keep using sendfile and we don't waste CPU re-compressing
// already-compressed bytes.
func binaryExt(path string) bool {
	switch {
	case strings.HasSuffix(path, ".woff2"), strings.HasSuffix(path, ".woff"),
		strings.HasSuffix(path, ".png"), strings.HasSuffix(path, ".jpg"),
		strings.HasSuffix(path, ".jpeg"), strings.HasSuffix(path, ".gif"),
		strings.HasSuffix(path, ".webp"), strings.HasSuffix(path, ".ico"),
		strings.HasSuffix(path, ".mp4"), strings.HasSuffix(path, ".webm"):
		return true
	}
	return false
}

type gzipRW struct {
	http.ResponseWriter
	gw      *gzip.Writer
	wrote   bool
	useGzip bool
}

func (g *gzipRW) WriteHeader(code int) {
	if g.wrote {
		return
	}
	g.wrote = true
	h := g.ResponseWriter.Header()
	// Only compress full 200 responses of a compressible type. Skipping 206/304
	// avoids corrupting Range/conditional responses.
	if code == http.StatusOK && compressibleType(h.Get("Content-Type")) {
		h.Del("Content-Length") // length changes after compression
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		g.useGzip = true
		g.gw = gzip.NewWriter(g.ResponseWriter)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipRW) Write(b []byte) (int, error) {
	if !g.wrote {
		g.WriteHeader(http.StatusOK)
	}
	if g.useGzip {
		return g.gw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipRW) Close() {
	if g.gw != nil {
		g.gw.Close()
	}
}

// gzipMiddleware transparently compresses eligible responses. It never touches
// WebSocket upgrades, Range requests, or already-compressed binary assets.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") ||
			r.Header.Get("Range") != "" ||
			binaryExt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		gz := &gzipRW{ResponseWriter: w}
		defer gz.Close()
		next.ServeHTTP(gz, r)
	})
}

// staticHandler serves ./static with cache headers: fingerprinted /assets/*
// are immutable (cache forever); everything else (index.html) is revalidated
// so new deploys are picked up immediately.
func staticHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fs.ServeHTTP(w, r)
	})
}
