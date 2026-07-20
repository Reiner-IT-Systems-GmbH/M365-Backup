package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	passHash [32]byte
	ttl      time.Duration

	// login rate limit (per client IP)
	attempts map[string][]time.Time
}

func NewSessionStore(adminPassword string) *SessionStore {
	return &SessionStore{
		sessions: map[string]time.Time{},
		passHash: sha256.Sum256([]byte(adminPassword)),
		ttl:      24 * time.Hour,
		attempts: map[string][]time.Time{},
	}
}

const (
	loginWindow      = time.Minute
	loginMaxAttempts = 10
)

func (s *SessionStore) allowLogin(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	cut := now.Add(-loginWindow)
	recent := s.attempts[ip][:0]
	for _, t := range s.attempts[ip] {
		if t.After(cut) {
			recent = append(recent, t)
		}
	}
	s.attempts[ip] = recent
	return len(recent) < loginMaxAttempts
}

func (s *SessionStore) recordLoginAttempt(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[ip] = append(s.attempts[ip], time.Now())
}

func (s *SessionStore) Login(password string) (token string, ok bool) {
	if !s.CheckPassword(password) {
		return "", false
	}
	token = uuid.NewString()
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return token, true
}

// CheckPassword verifies the admin password without creating a session.
func (s *SessionStore) CheckPassword(password string) bool {
	got := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(got[:], s.passHash[:]) == 1
}

func (s *SessionStore) Valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[token]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, token)
		return false
	}
	return true
}

func (s *SessionStore) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *SessionStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/login") ||
			strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/api/consent/callback") ||
			r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie("session")
		if err != nil || !s.Valid(c.Value) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
