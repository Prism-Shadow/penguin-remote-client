package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExplicitSystemNetworkUsesSecretOnlyInProxyHeader(t *testing.T) {
	secret := "private-bridge-secret"
	var count atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer device-secret" {
			t.Error("device token missing")
		}
		w.Write([]byte(`{"protocolVersion":2,"device":{"deviceId":"d"},"entitlement":{"status":"active"}}`))
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("remote:"+secret)) {
			http.Error(w, "proxy authentication failed", 407)
			return
		}
		if strings.Contains(r.URL.String(), secret) {
			t.Error("secret reached URL")
		}
		count.Add(1)
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "connect failed", 502)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { io.Copy(upstream, client); client.Close(); upstream.Close() }()
		go func() { io.Copy(client, upstream); client.Close(); upstream.Close() }()
	}))
	defer proxy.Close()
	n := NetworkConfig{SystemProxyURL: proxy.URL, SystemProxySecret: secret}
	h, err := n.HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(target.Certificate())
	h.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots}
	defer h.CloseIdleConnections()
	cli, _ := remoteapi.NewClient(remoteapi.ClientConfig{BaseURL: target.URL, HTTPClient: h})
	if _, err := cli.Status(context.Background(), "device-secret"); err != nil {
		t.Fatal("explicit proxy request failed")
	}
	if count.Load() != 1 {
		t.Fatal("selected proxy bypassed")
	}
	data := n.SystemProxyURL
	if strings.Contains(data, secret) {
		t.Fatal("bridge secret in URL")
	}
}
func TestSystemBridgeMustBePrivateAndCannotSilentlyBypass(t *testing.T) {
	for _, address := range []string{"http://proxy.test:8080", "socks5://127.0.0.1:8080", "http://user:password@127.0.0.1:8080", "http://127.0.0.1", "http://127.0.0.1:8080/path"} {
		if _, err := (NetworkConfig{SystemProxyURL: address}).HTTPClient(); err == nil {
			t.Fatal("invalid system bridge accepted")
		}
	}
}

func TestLegacyNetworkPreferencesAreIgnored(t *testing.T) {
	var n NetworkConfig
	if err := json.Unmarshal([]byte(`{"mode":"manual","proxyUrl":"http://user:private-secret@proxy.test:8080"}`), &n); err != nil {
		t.Fatal(err)
	}
	n.SystemProxyURL = "http://127.0.0.1:12345"
	n.SystemProxySecret = "private-bridge-secret"
	p, err := n.ProxyForURL("https://frps.remote.test:7000")
	if err != nil || p.String() != n.SystemProxyURL {
		t.Fatal("legacy preferences overrode the system route")
	}
	data, err := json.Marshal(n)
	if err != nil || string(data) != "{}" {
		t.Fatal("network preferences or private bridge data were persisted")
	}
}

func TestNativeNetworkFollowsProxyEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, bypass := range []bool{false, true} {
		command := exec.Command(executable, "-test.run=^TestNativeNetworkEnvironmentHelper$")
		for _, item := range os.Environ() {
			key, _, _ := strings.Cut(item, "=")
			switch strings.ToUpper(key) {
			case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "REQUEST_METHOD", "REMOTE_NETWORK_ENV_TEST":
				continue
			}
			command.Env = append(command.Env, item)
		}
		mode, noProxy := "proxy", ""
		if bypass {
			mode, noProxy = "bypass", "remote-network.test"
		}
		command.Env = append(command.Env, "REMOTE_NETWORK_ENV_TEST="+mode, "HTTP_PROXY=http://127.0.0.1:18888", "HTTPS_PROXY=http://127.0.0.1:18888", "NO_PROXY="+noProxy)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("native environment routing failed: %s", output)
		}
	}
}

func TestNativeNetworkEnvironmentHelper(t *testing.T) {
	mode := os.Getenv("REMOTE_NETWORK_ENV_TEST")
	if mode == "" {
		return
	}
	n := NetworkConfig{}
	target := "https://remote-network.test:7000"
	p, err := n.ProxyForURL(target)
	if err != nil || (p == nil) != (mode == "bypass") {
		t.Fatal("tunnel ignored the proxy environment or NO_PROXY")
	}
	if p != nil && p.String() != "http://127.0.0.1:18888" {
		t.Fatal("tunnel selected the wrong environment proxy")
	}
	h, err := n.HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, target, nil)
	hp, err := h.Transport.(*http.Transport).Proxy(req)
	if err != nil || (hp == nil) != (p == nil) || (hp != nil && hp.String() != p.String()) {
		t.Fatal("HTTP and tunnel use different environment routing")
	}
}

func TestSystemBridgeAlsoTunnelsHTTPAndStopsPendingHandshake(t *testing.T) {
	entered := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "CONNECT" || req.Header.Get("Proxy-Authorization") == "" {
			t.Error("request bypassed authenticated CONNECT")
		}
		entered <- struct{}{}
		conn, _, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		data := make([]byte, 4096)
		_, _ = conn.Read(data)
		if strings.Contains(string(data), "private-bridge-secret") {
			t.Error("bridge secret reached application HTTP request")
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
	}))
	defer proxy.Close()
	h, err := (NetworkConfig{SystemProxyURL: proxy.URL, SystemProxySecret: "private-bridge-secret"}).HTTPClient()
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.Get("http://go.remote.test/api/remote/client/status")
	if err != nil {
		t.Fatal("HTTP target did not use broker")
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(data) != "ok" {
		t.Fatal("tunneled HTTP failed")
	}
	<-entered
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { entered <- struct{}{}; <-req.Context().Done() }))
	defer stall.Close()
	h, _ = (NetworkConfig{SystemProxyURL: stall.URL, SystemProxySecret: "secret"}).HTTPClient()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://go.remote.test/status", nil)
		_, err := h.Do(req)
		done <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("pending CONNECT ignored cancellation")
	}
}
