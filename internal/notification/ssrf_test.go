package notification

import (
	"net"
	"testing"
)

func TestValidateWebhookURL(t *testing.T) {
	ok := []string{
		"http://127.0.0.1:8080/hook",
		"https://hooks.slack.com/services/T00/B00/xxx",
		"http://example.com/webhook",
	}
	for _, u := range ok {
		if err := ValidateWebhookURL(u); err != nil {
			t.Fatalf("%q: %v", u, err)
		}
	}
	bad := []string{
		"",
		"ftp://example.com/x",
		"https://metadata.google.internal/computeMetadata/v1/",
		"http://169.254.169.254/latest/meta-data/",
		"https://user:pass@example.com/hook",
	}
	for _, u := range bad {
		if err := ValidateWebhookURL(u); err == nil {
			t.Fatalf("%q: expected error", u)
		}
	}
}

func TestIsBlockedWebhookIP(t *testing.T) {
	if !isBlockedWebhookIP(net.ParseIP("169.254.169.254")) {
		t.Fatal("metadata IP should be blocked")
	}
	if isBlockedWebhookIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback should be allowed for self-hosted hooks")
	}
	if isBlockedWebhookIP(net.ParseIP("10.0.0.5")) {
		t.Fatal("private RFC1918 should be allowed")
	}
}
