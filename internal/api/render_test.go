package api

import "testing"

func TestSafeLocalPath(t *testing.T) {
	cases := map[string]string{
		"/tenants":                        "/tenants",
		"/tenants?x=1":                    "/tenants?x=1",
		"/tenants/abc/browser?service=ex": "/tenants/abc/browser?service=ex",
		"/settings":                       "/settings",
		"/login":                          "/login",
		"/openapi":                        "/openapi",
		"/openapi.yaml":                   "/openapi.yaml",
		"/tenants/../etc":                 "",
		"//evil.com":                      "",
		"https://evil.com":                "",
		"/\\evil.com":                     "",
		"/ok\r\nSet-Cookie":               "",
		"":                                "",
		"tenants":                         "",
		"/evil":                           "",
		"/tenants@evil.com":               "",
	}
	for in, want := range cases {
		if got := safeLocalPath(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}
