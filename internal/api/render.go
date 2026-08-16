package api

import (
	"net/http"
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
	// Only string literals — never a user-provided URL (CodeQL go/unvalidated-url-redirection).
	http.Redirect(w, r, langRedirectTarget(r.URL.Query().Get("next")), http.StatusFound)
}

func langRedirectTarget(next string) string {
	next = strings.TrimSpace(next)
	if i := strings.IndexAny(next, "?#"); i >= 0 {
		next = next[:i]
	}
	switch next {
	case "/settings":
		return "/settings"
	case "/login":
		return "/login"
	case "/openapi":
		return "/openapi"
	case "/openapi.yaml":
		return "/openapi.yaml"
	default:
		return "/tenants"
	}
}

func safeLocalPath(p string) string {
	return langRedirectTarget(p)
}
