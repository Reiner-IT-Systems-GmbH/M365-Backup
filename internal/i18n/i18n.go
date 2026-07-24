// Package i18n provides DE/EN strings for the admin UI.
package i18n

import (
	"net/http"
	"strings"
)

const (
	DE         = "de"
	EN         = "en"
	CookieName = "lang"
	cookieMax  = 365 * 24 * 60 * 60
)

// Catalog resolves UI strings for one language.
type Catalog struct {
	Lang string
}

// New returns a catalog for lang (falls back to DE).
func New(lang string) Catalog {
	return Catalog{Lang: Normalize(lang)}
}

// T returns the translation for key; missing keys fall back to DE, then the key.
func (c Catalog) T(key string) string {
	if m := catalogs[c.Lang]; m != nil {
		if s, ok := m[key]; ok {
			return s
		}
	}
	if c.Lang != DE {
		if s, ok := catalogs[DE][key]; ok {
			return s
		}
	}
	return key
}

// Normalize maps aliases to de|en.
func Normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch {
	case lang == EN || strings.HasPrefix(lang, "en"):
		return EN
	default:
		return DE
	}
}

// FromRequest: cookie → Accept-Language → DE.
func FromRequest(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return Normalize(c.Value)
	}
	return Normalize(parseAcceptLanguage(r.Header.Get("Accept-Language")))
}

// SetCookie stores the UI language preference.
func SetCookie(w http.ResponseWriter, lang string) {
	lang = Normalize(lang)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    lang,
		Path:     "/",
		MaxAge:   cookieMax,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	})
}

func parseAcceptLanguage(h string) string {
	if h == "" {
		return DE
	}
	// Pick first tag; prefer en if listed before de with higher or equal q (simple split).
	best := DE
	bestQ := -1.0
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, q := part, 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			q = 0
			rest := part[i+1:]
			if j := strings.Index(strings.ToLower(rest), "q="); j >= 0 {
				var f float64
				if _, err := parseQ(rest[j+2:], &f); err == nil {
					q = f
				}
			}
		}
		tag = strings.ToLower(tag)
		var cand string
		switch {
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			cand = EN
		case tag == "de" || strings.HasPrefix(tag, "de-"):
			cand = DE
		default:
			continue
		}
		if q > bestQ {
			bestQ = q
			best = cand
		}
	}
	return best
}

func parseQ(s string, out *float64) (int, error) {
	s = strings.TrimSpace(s)
	n := 0
	var v float64
	var frac float64
	var div float64
	seenDot := false
	for i, c := range s {
		if c >= '0' && c <= '9' {
			if !seenDot {
				v = v*10 + float64(c-'0')
			} else {
				div *= 10
				frac = frac*10 + float64(c-'0')
			}
			n = i + 1
			continue
		}
		if c == '.' && !seenDot {
			seenDot = true
			div = 1
			n = i + 1
			continue
		}
		break
	}
	if seenDot && div > 0 {
		v += frac / div
	}
	*out = v
	return n, nil
}

var catalogs = map[string]map[string]string{
	DE: de,
	EN: en,
}
