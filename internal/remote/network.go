package remote

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

type NetworkConfig struct {
	SystemProxyURL    string `json:"-"`
	SystemProxySecret string `json:"-"`
}

// ProxyForURL follows Electron's system/PAC bridge in desktop builds. Native
// CLI commands use the process proxy environment, including NO_PROXY.
func (n NetworkConfig) ProxyForURL(target string) (*url.URL, error) {
	if n.SystemProxyURL == "" {
		u, err := url.Parse(target)
		if err != nil {
			return nil, errors.New("system_proxy_unavailable")
		}
		return http.ProxyFromEnvironment(&http.Request{URL: u})
	}
	u, err := url.Parse(n.SystemProxyURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("system_proxy_unavailable")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("system_proxy_unavailable")
	}
	return u, nil
}
func (n NetworkConfig) HTTPClient() (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = http.ProxyFromEnvironment
	if n.SystemProxyURL != "" {
		p, err := n.ProxyForURL("https://go.penguin.ooo")
		if err != nil {
			return nil, err
		}
		t.Proxy = nil
		// Tunnel both HTTP and HTTPS through the private broker. net/http's
		// normal proxy path only uses CONNECT for HTTPS; the broker is TCP-only.
		t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialSystemBridge(ctx, p.Host, address, n.SystemProxySecret)
		}
	}
	return &http.Client{Transport: t, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

type bufferedBridgeConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedBridgeConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

func dialSystemBridge(ctx context.Context, bridge, address, secret string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", bridge)
	if err != nil {
		return nil, errors.New("system_proxy_unavailable")
	}
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	req := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
	if secret != "" {
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("remote:"+secret)))
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, errors.New("system_proxy_unavailable")
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil || resp.StatusCode != http.StatusOK || ctx.Err() != nil {
		conn.Close()
		return nil, errors.New("system_proxy_unavailable")
	}
	stop()
	_ = conn.SetDeadline(time.Time{})
	return &bufferedBridgeConn{Conn: conn, reader: reader}, nil
}
