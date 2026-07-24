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
	next := safeLocalPath(r.URL.Query().Get("next"))
	if next == "" {
		if ref := r.Referer(); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				next = safeLocalPath(u.RequestURI())
			}
		}
	}
	if next == "" {
		next = "/tenants"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func safeLocalPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return ""
	}
	if strings.Contains(p, "://") || strings.ContainsAny(p, "\r\n") {
		return ""
	}
	return p
}
