package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/rhw/m365backup/internal/db"
)

const (
	scopeRead  = "read"
	scopeWrite = "write"
	tokenCost  = bcrypt.DefaultCost
)

type ctxKey int

const principalKey ctxKey = 1

// Principal is the authenticated UI user or API token.
type Principal struct {
	UserID   string
	Username string
	Scope    string // read | write
	Via      string // session | token
}

func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

func withPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

type sessionInfo struct {
	UserID   string
	Username string
	Expires  time.Time
}

type SessionStore struct {
	DB       *db.DB
	mu       sync.Mutex
	sessions map[string]sessionInfo
	ttl      time.Duration
	attempts map[string][]time.Time
}

// EnsureBootstrapAuth creates or updates the env admin user.
func EnsureBootstrapAuth(ctx context.Context, database *db.DB, username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	u, err := database.UpsertUser(ctx, username, hash)
	if err != nil {
		return err
	}
	return database.DeleteAPITokensByKind(ctx, u.ID, "env")
}

func NewSessionStore(database *db.DB) *SessionStore {
	return &SessionStore{
		DB:       database,
		sessions: map[string]sessionInfo{},
		ttl:      24 * time.Hour,
		attempts: map[string][]time.Time{},
	}
}

const (
	loginWindow      = time.Minute
	loginMaxAttempts = 10
)

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), tokenCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPasswordHash(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func hashAPIToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func newAPITokenPlain() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "m365_" + hex.EncodeToString(b), nil
}

func tokenPrefix(plain string) string {
	if len(plain) < 12 {
		return plain + "…"
	}
	return plain[:12] + "…"
}

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

func (s *SessionStore) Login(ctx context.Context, username, password string) (token string, ok bool) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
		return "", false
	}
	u, err := s.verifyUser(ctx, username, password)
	if err != nil {
		return "", false
	}
	token = uuid.NewString()
	s.mu.Lock()
	s.sessions[token] = sessionInfo{UserID: u.ID, Username: u.Username, Expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return token, true
}

func (s *SessionStore) verifyUser(ctx context.Context, username, password string) (*db.User, error) {
	if s.DB == nil {
		return nil, errAuth
	}
	u, err := s.DB.GetUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
		return nil, err
	}
	if !CheckPasswordHash(u.PasswordHash, password) {
		return nil, errAuth
	}
	return u, nil
}

func (s *SessionStore) VerifyUserPassword(ctx context.Context, userID, password string) bool {
	if s.DB == nil || userID == "" {
		return false
	}
	u, err := s.DB.GetUser(ctx, userID)
	if err != nil {
		return false
	}
	return CheckPasswordHash(u.PasswordHash, password)
}

func (s *SessionStore) Valid(token string) (*Principal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.sessions[token]
	if !ok || time.Now().After(info.Expires) {
		delete(s.sessions, token)
		return nil, false
	}
	return &Principal{UserID: info.UserID, Username: info.Username, Scope: scopeWrite, Via: "session"}, true
}

func (s *SessionStore) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *SessionStore) AuthenticateBearer(ctx context.Context, raw string) (*Principal, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || s.DB == nil {
		return nil, false
	}
	tok, err := s.DB.GetAPITokenByHash(ctx, hashAPIToken(raw))
	if err != nil {
		return nil, false
	}
	u, uerr := s.DB.GetUser(ctx, tok.UserID)
	if uerr != nil {
		return nil, false
	}
	s.DB.TouchAPIToken(ctx, tok.ID)
	return &Principal{UserID: u.ID, Username: u.Username, Scope: tok.Scope, Via: "token"}, true
}

func (s *SessionStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/login") ||
			strings.HasPrefix(r.URL.Path, "/lang/") ||
			strings.HasPrefix(r.URL.Path, "/static/") ||
			strings.HasPrefix(r.URL.Path, "/api/consent/callback") ||
			r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if p := s.principalFromRequest(r); p != nil {
			if !allowScope(r, p) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

func (s *SessionStore) principalFromRequest(r *http.Request) *Principal {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		raw := strings.TrimSpace(h[7:])
		if p, ok := s.AuthenticateBearer(r.Context(), raw); ok {
			return p
		}
	}
	c, err := r.Cookie("session")
	if err != nil {
		return nil
	}
	p, ok := s.Valid(c.Value)
	if !ok {
		return nil
	}
	return p
}

func allowScope(r *http.Request, p *Principal) bool {
	if p.Scope == scopeWrite {
		return true
	}
	if p.Scope != scopeRead {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if strings.HasPrefix(r.URL.Path, "/api/consent/start") {
			return false
		}
		return true
	default:
		return false
	}
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

type authError string

func (e authError) Error() string { return string(e) }

const errAuth authError = "unauthorized"
