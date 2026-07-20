package api

import (
	"strings"
	"testing"

	"github.com/rhw/m365backup/internal/db"
)

func TestPublicTenantOmitsSecrets(t *testing.T) {
	got := publicTenant(db.Tenant{
		Name: "t", ClientSecret: "enc-secret", KopiaPassword: "enc-kopia",
	})
	if got.ClientSecret != "" || got.KopiaPassword != "" {
		t.Fatalf("secrets not redacted: %+v", got)
	}
	if got.Name != "t" {
		t.Fatal("name should remain")
	}
}

func TestRedactNotificationConfig(t *testing.T) {
	in := `{"host":"smtp.example","password":"secret","user_key":"uk","app_token":"at","url":"https://hooks.example/x","headers":{"Authorization":"Bearer x","X-Custom":"ok"}}`
	out := redactNotificationConfig(in)
	if strings.Contains(out, "secret") || strings.Contains(out, "Bearer") {
		t.Fatalf("leaked secret material: %s", out)
	}
	if !strings.Contains(out, "smtp.example") || !strings.Contains(out, "hooks.example") {
		t.Fatalf("non-secret fields missing: %s", out)
	}
	if !strings.Contains(out, `"X-Custom":"ok"`) && !strings.Contains(out, `"X-Custom": "ok"`) {
		// json.Marshal has no spaces
		if !strings.Contains(out, `"X-Custom":"ok"`) {
			t.Fatalf("custom header should remain: %s", out)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := NewSessionStore("test-password-ok")
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
	_, ok := s.Login("wrong")
	if ok {
		t.Fatal("wrong password must fail")
	}
	tok, ok := s.Login("test-password-ok")
	if !ok || tok == "" {
		t.Fatal("correct password must succeed")
	}
}
