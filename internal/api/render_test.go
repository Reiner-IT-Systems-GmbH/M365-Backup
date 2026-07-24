package api

import "testing"

func TestSafeLocalPath(t *testing.T) {
	cases := map[string]string{
		"/tenants":           "/tenants",
		"/tenants?x=1":       "/tenants?x=1",
		"//evil.com":         "",
		"https://evil.com":   "",
		"/ok\r\nSet-Cookie":  "",
		"":                   "",
		"tenants":            "",
	}
	for in, want := range cases {
		if got := safeLocalPath(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}
