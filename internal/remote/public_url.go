package remote

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// TunnelPublicURL uses only trusted tunnel configuration, never a request Host.
// Older custom-domain configs omitted the URL and used HTTPS by default.
func (c Config) TunnelPublicURL() (string, error) {
	if c.Tunnel == nil {
		return "", fmt.Errorf("tunnel config is required")
	}
	raw := strings.TrimSpace(c.Tunnel.PublicURL)
	if raw == "" {
		if c.Tunnel.CustomDomain == "" {
			return "", fmt.Errorf("tunnel.publicUrl is required when only a subdomain is configured")
		}
		raw = "https://" + c.Tunnel.CustomDomain
	}
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(raw, " \t\r\n,\\%") {
		return "", fmt.Errorf("tunnel.publicUrl is not a valid HTTP origin")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("tunnel.publicUrl must contain only an HTTP scheme and authority")
	}
	if text := u.Port(); text != "" {
		port, err := strconv.Atoi(text)
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("tunnel.publicUrl has an invalid port")
		}
	}
	if c.Tunnel.CustomDomain != "" && !strings.EqualFold(u.Hostname(), c.Tunnel.CustomDomain) {
		return "", fmt.Errorf("tunnel.publicUrl does not match tunnel.customDomain")
	}
	u.Path = ""
	return u.String(), nil
}
