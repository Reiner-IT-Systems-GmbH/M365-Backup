package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/rhw/m365backup/internal/i18n"
)

// render executes a template with Lang, L (catalog), and ReturnTo injected.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	lang := i18n.FromRequest(r)
	data["Lang"] = lang
	data["L"] = i18n.New(lang)
	if _, ok := data["ReturnTo"]; !ok {
		data["ReturnTo"] = r.URL.RequestURI()
	}
	_ = s.Templates.ExecuteTemplate(w, name, data)
}

func (s *Server) handleSetLang(w http.ResponseWriter, r *http.Request) {
	i18n.SetCookie(w, chi.URLParam(r, "lang"))
	raw := strings.TrimSpace(r.URL.Query().Get("next"))
	if raw == "" {
		if ref := r.Referer(); ref != "" {
			if ru, err := url.Parse(ref); err == nil {
				raw = ru.RequestURI()
			}
		}
	}
	// Parse + empty host/scheme in this function so CodeQL go/unvalidated-url-redirection
	// sees a sanitizer on the same value that reaches Redirect.
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Scheme != "" || u.Opaque != "" || u.User != nil {
		http.Redirect(w, r, "/tenants", http.StatusFound)
		return
	}
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n") || strings.Contains(u.Path, "..") {
		http.Redirect(w, r, "/tenants", http.StatusFound)
		return
	}
	if !isAppLocalPath(u.Path) {
		http.Redirect(w, r, "/tenants", http.StatusFound)
		return
	}
	http.Redirect(w, r, u.RequestURI(), http.StatusFound)
}

func safeLocalPath(p string) string {
	p = strings.TrimSpace(p)
	u, err := url.Parse(p)
	if err != nil || u.IsAbs() || u.Host != "" || u.Scheme != "" || u.Opaque != "" || u.User != nil {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\r\n") || strings.Contains(u.Path, "..") {
		return ""
	}
	if !isAppLocalPath(u.Path) {
		return ""
	}
	return u.RequestURI()
}

func isAppLocalPath(path string) bool {
	switch {
	case path == "/tenants", strings.HasPrefix(path, "/tenants/"):
		return true
	case path == "/settings", strings.HasPrefix(path, "/settings/"):
		return true
	case path == "/login":
		return true
	case path == "/openapi", path == "/openapi.yaml", strings.HasPrefix(path, "/openapi/"):
		return true
	default:
		return false
	}
}
