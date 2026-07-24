package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalize(t *testing.T) {
	if Normalize("en-US") != EN {
		t.Fatal("en-US")
	}
	if Normalize("de_DE") != DE {
		t.Fatal("de_DE")
	}
	if Normalize("fr") != DE {
		t.Fatal("fallback")
	}
}

func TestCatalogFallback(t *testing.T) {
	c := New(EN)
	if c.T("nav.settings") == "" || c.T("nav.settings") == "nav.settings" {
		t.Fatalf("missing en: %q", c.T("nav.settings"))
	}
	if New(DE).T("nav.settings") == New(EN).T("nav.settings") {
		t.Fatal("de and en should differ for nav.settings")
	}
}

func TestFromRequestCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "en"})
	if FromRequest(r) != EN {
		t.Fatal(FromRequest(r))
	}
}

func TestAcceptLanguage(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9,de;q=0.8")
	if FromRequest(r) != EN {
		t.Fatal(FromRequest(r))
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("Accept-Language", "de-DE,de;q=0.9")
	if FromRequest(r2) != DE {
		t.Fatal(FromRequest(r2))
	}
}
