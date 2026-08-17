package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rhw/m365backup/internal/db"
)

func testAuthDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: filepath.Join(t.TempDir(), "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestBootstrapLoginAndBearer(t *testing.T) {
	database := testAuthDB(t)
	ctx := context.Background()
	if err := EnsureBootstrapAuth(ctx, database, "m365adminuser", "test-password-ok"); err != nil {
		t.Fatal(err)
	}
	s := NewSessionStore(database)
	if _, ok := s.Login(ctx, "m365adminuser", "wrong"); ok {
		t.Fatal("wrong password")
	}
	tok, ok := s.Login(ctx, "m365adminuser", "test-password-ok")
	if !ok || tok == "" {
		t.Fatal("login")
	}
	if p, ok := s.Valid(tok); !ok || p.Username != "m365adminuser" || p.Scope != scopeWrite {
		t.Fatalf("session %+v ok=%v", p, ok)
	}
	p, ok := s.AuthenticateBearer(ctx, "test-password-ok")
	if !ok || p.Via != "password" || p.Scope != scopeWrite {
		t.Fatalf("password-as-token %+v ok=%v", p, ok)
	}
	if _, ok := s.Login(ctx, "", "test-password-ok"); !ok {
		t.Fatal("password-only login (Usage-Sync / scripts) must still work")
	}
	if _, ok := s.Login(ctx, "", "wrong"); ok {
		t.Fatal("empty user + wrong password")
	}
}

func TestAPITokenScopes(t *testing.T) {
	database := testAuthDB(t)
	ctx := context.Background()
	if err := EnsureBootstrapAuth(ctx, database, "m365adminuser", "test-password-ok"); err != nil {
		t.Fatal(err)
	}
	s := NewSessionStore(database)
	u, err := database.GetUserByUsername(ctx, "m365adminuser")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := newAPITokenPlain()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.InsertAPIToken(ctx, &db.APIToken{
		UserID: u.ID, Name: "ci", Kind: "user",
		TokenHash: hashAPIToken(plain), Prefix: tokenPrefix(plain), Scope: scopeRead,
	}); err != nil {
		t.Fatal(err)
	}
	p, ok := s.AuthenticateBearer(ctx, plain)
	if !ok || p.Scope != scopeRead {
		t.Fatalf("token %+v ok=%v", p, ok)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/tenants", nil)
	if !allowScope(req, p) {
		t.Fatal("read GET should pass")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/tenants", nil)
	if allowScope(req, p) {
		t.Fatal("read POST should fail")
	}
}

func TestReadLoginCredentials(t *testing.T) {
	form := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=secret-pass"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	u, p := readLoginCredentials(form)
	if u != "" || p != "secret-pass" {
		t.Fatalf("form password-only: user=%q pass=%q", u, p)
	}

	js := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"password":"json-pass"}`))
	js.Header.Set("Content-Type", "application/json")
	u, p = readLoginCredentials(js)
	if u != "" || p != "json-pass" {
		t.Fatalf("json password-only: user=%q pass=%q", u, p)
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := NewSessionStore(nil)
	ip := "203.0.113.10"
	for i := 0; i < loginMaxAttempts; i++ {
		if !s.allowLogin(ip) {
			t.Fatalf("attempt %d should be allowed", i)
		}
		s.recordLoginAttempt(ip)
	}
	if s.allowLogin(ip) {
		t.Fatal("should be rate limited")
	}
}

func TestBcryptNotSHA256(t *testing.T) {
	h, err := HashPassword("test-password-ok")
	if err != nil {
		t.Fatal(err)
	}
	if h[:4] != "$2a$" && h[:4] != "$2b$" {
		t.Fatalf("expected bcrypt hash, got %s", h[:7])
	}
	if !CheckPasswordHash(h, "test-password-ok") {
		t.Fatal("compare")
	}
	if CheckPasswordHash(h, "nope") {
		t.Fatal("wrong password matched")
	}
}
