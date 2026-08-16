package api

import "testing"

func TestLangRedirectTarget(t *testing.T) {
	cases := map[string]string{
		"/settings":                       "/settings",
		"/settings?x=1":                   "/settings",
		"/login":                          "/login",
		"/openapi":                        "/openapi",
		"/openapi.yaml":                   "/openapi.yaml",
		"/tenants":                        "/tenants",
		"/tenants/abc/browser?service=ex": "/tenants",
		"//evil.com":                      "/tenants",
		"https://evil.com":                "/tenants",
		"/evil":                           "/tenants",
		"":                                "/tenants",
	}
	for in, want := range cases {
		if got := langRedirectTarget(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}
