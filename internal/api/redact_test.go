package api

import (
	"strings"
	"testing"

	"github.com/rhw/m365backup/internal/db"
)

func TestPublicTenantOmitsSecrets(t *testing.T) {
	got := publicTenant(db.Tenant{
		Name: "t", ClientSecret: "enc-secret", StorePassword: "enc-store",
	})
	if got.ClientSecret != "" || got.StorePassword != "" {
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
