package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"homelab/config"
)

const (
	sessionCookie = "hl_session"
	sessionTTL    = 7 * 24 * time.Hour
)

var (
	sessions      = map[string]time.Time{} // token -> expiry
	sessionsMu    sync.Mutex
	secureCookies bool
)

// SetSecureCookies marks session cookies Secure; call with true when serving
// over HTTPS so the cookie is never sent in cleartext.
func SetSecureCookies(v bool) { secureCookies = v }

// AuthEnabled reports whether credentials are configured. When false, the
// middleware is a no-op and the dashboard is open (with a startup warning).
func AuthEnabled() bool {
	return config.C.AuthUser != "" && config.C.AuthPasswordHash != ""
}

// HashPassword returns a bcrypt hash for use as auth_password_hash in the
// config. Exposed so `homelab -hashpw` can generate it.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	exp, ok := sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(sessions, c.Value)
		return false
	}
	return true
}

// RequireAuth wraps a handler so it only runs for authenticated requests.
// No-op when auth is not configured.
func RequireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if AuthEnabled() && !validSession(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// LoginHandler — POST /api/login {username, password}
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if !AuthEnabled() {
		writeJSON(w, map[string]any{"status": "ok"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(config.C.AuthUser)) == 1
	// bcrypt is intentionally slow, which throttles brute-force on its own.
	passErr := bcrypt.CompareHashAndPassword([]byte(config.C.AuthPasswordHash), []byte(req.Password))
	if !userOK || passErr != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := newToken()
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	sessionsMu.Lock()
	sessions[token] = time.Now().Add(sessionTTL)
	sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	writeJSON(w, map[string]any{"status": "ok", "user": config.C.AuthUser})
}

// LogoutHandler — POST /api/logout
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		sessionsMu.Lock()
		delete(sessions, c.Value)
		sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, map[string]any{"status": "ok"})
}

// MeHandler — GET /api/me. Always 200; the frontend uses this to decide whether
// to show the login screen.
func MeHandler(w http.ResponseWriter, r *http.Request) {
	authed := !AuthEnabled() || validSession(r)
	user := ""
	if AuthEnabled() && authed {
		user = config.C.AuthUser
	}
	writeJSON(w, map[string]any{
		"auth_enabled":  AuthEnabled(),
		"authenticated": authed,
		"user":          user,
	})
}
