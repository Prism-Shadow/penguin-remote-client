package localproxy

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type origin struct {
	scheme string
	host   string
	port   int
}

// Compare origins, not domains or request Host headers. Default ports and DNS
// letter case do not distinguish origins; paths, credentials and lists are invalid.
func parseOrigin(raw string) (origin, error) {
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(raw, " \t\r\n,\\%") {
		return origin{}, fmt.Errorf("invalid origin")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") {
		return origin{}, fmt.Errorf("origin must contain only an HTTP scheme and authority")
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if explicit := u.Port(); explicit != "" {
		port, err = strconv.Atoi(explicit)
		if err != nil || port < 1 || port > 65535 {
			return origin{}, fmt.Errorf("invalid origin port")
		}
	}
	return origin{scheme: u.Scheme, host: strings.ToLower(u.Hostname()), port: port}, nil
}

func (o origin) allows(values []string) bool {
	if len(values) == 0 {
		return true // Preserve non-browser requests without inventing an Origin.
	}
	if len(values) != 1 {
		return false
	}
	incoming, err := parseOrigin(values[0])
	return err == nil && incoming == o
}
