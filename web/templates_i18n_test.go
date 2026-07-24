package web_test

import (
	"bytes"
	"encoding/json"
	"html/template"
	"testing"
	"time"

	"github.com/rhw/m365backup/internal/i18n"
	"github.com/rhw/m365backup/web"
)

func TestTemplatesParseAndRender(t *testing.T) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"fmtTime": func(tm time.Time, layout string) string { return tm.Format(layout) },
		"toJSON": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(b), nil
		},
	}).ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	pages := []string{
		"login.html", "tenants.html", "tenant_new.html", "settings.html", "openapi.html",
		"restore.html", "job_detail.html", "browser.html", "tenant_recovery.html",
		"snapshot_browse.html", "tenant_detail.html", "jobs_partial.html",
		"job_live_partial.html", "snapshots_partial.html", "pst_exports_partial.html",
		"pst_folders_partial.html",
	}
	type tenant struct{ ID, Name, AzureTenantID, Status string }
	type job struct {
		ID, Status, Service, ProgressMessage, ErrorMessage, KopiaSnapshot string
		ProgressPct, ItemsNew, ItemsTotal                                 int
		BytesTransferred                                                    int64
		CreatedAt                                                           time.Time
	}
	for _, lang := range []string{"de", "en"} {
		for _, name := range pages {
			var buf bytes.Buffer
			data := map[string]any{
				"Lang": lang, "L": i18n.New(lang), "ReturnTo": "/tenants",
				"RedirectURI": "http://localhost/callback",
				"BaseURL":     "http://localhost",
				"Tenant":      tenant{"t1", "Acme", "az", "active"},
				"TenantID":    "t1",
				"Job":         job{ID: "j1", Status: "success", Service: "exchange", CreatedAt: time.Now()},
				"SnapID":      "s1",
				"Services":    []string{"exchange"},
				"Service":     "exchange",
				"Version":     "live",
				"HasLive":     true,
			}
			if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
				t.Fatalf("%s/%s: %v", lang, name, err)
			}
		}
	}
}
