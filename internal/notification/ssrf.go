package notification

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// blockedWebhookHosts are well-known cloud metadata / SSRF targets.
var blockedWebhookHosts = map[string]struct{}{
	"metadata.google.internal": {},
	"metadata.goog":            {},
	"kubernetes.default":       {},
	"kubernetes.default.svc":   {},
}

// ValidateWebhookURL checks scheme and rejects known metadata hosts / link-local literal IPs.
// Hostname resolution is enforced at dial time by safeWebhookTransport (DNS-rebinding defense).
// Private RFC1918 and loopback remain allowed so self-hosted Slack/n8n webhooks keep working.
func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("webhook url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("webhook url must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook url missing host")
	}
	if u.User != nil {
		return fmt.Errorf("webhook url must not contain credentials")
	}
	if _, blocked := blockedWebhookHosts[strings.ToLower(host)]; blocked {
		return fmt.Errorf("webhook host not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedWebhookIP(ip) {
			return fmt.Errorf("webhook destination not allowed")
		}
	}
	return nil
}

func isBlockedWebhookIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Link-local (includes AWS/GCP/Azure metadata 169.254.169.254)
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		// "This host on this network" / broadcast-ish
		if ip4[0] == 0 {
			return true
		}
		return false
	}
	// IPv6 link-local fe80::/10 and unique-local is allowed except known metadata.
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// AWS IMDS IPv6
	if ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	return false
}

// safeWebhookTransport rejects dials to blocked IPs (DNS rebinding defense).
func safeWebhookTransport() *http.Transport {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ia := range ips {
			if isBlockedWebhookIP(ia.IP) {
				lastErr = fmt.Errorf("webhook destination not allowed")
				continue
			}
			d := net.Dialer{Timeout: 10 * time.Second}
			c, err := d.DialContext(ctx, network, net.JoinHostPort(ia.IP.String(), port))
			if err == nil {
				return c, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("webhook destination not allowed")
		}
		return nil, lastErr
	}
	return base
}

func newWebhookHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: safeWebhookTransport(),
	}
}
