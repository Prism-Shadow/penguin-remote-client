package localproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
)

type fakeIssuer struct {
	issued int
}

func (f *fakeIssuer) IssueRemoteSession(context.Context) (harnessapi.RemoteSession, error) {
	f.issued++
	return testSession("issued-session"), nil
}

func (f *fakeIssuer) ValidateSession(_ context.Context, token string) (bool, error) {
	return token == "valid-session", nil
}

func TestProxyBootstrapsCookieAndInjectsItUpstream(t *testing.T) {
	upstream := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(harnessapi.SessionCookieName)
		if err != nil || cookie.Value != "initial-session" {
			http.Error(w, "missing injected session", http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Upstream-Host", r.Host)
		w.Header().Set("X-Upstream-Origin", r.Header.Get("Origin"))
		w.Header().Set("X-Upstream-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		_, _ = io.WriteString(w, "ok")
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()

	issuer := &fakeIssuer{}
	p, err := Start("http://"+listener.Addr().String(), "", "https://device.remote.example.com", issuer, testSession("initial-session"))
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer p.Close(context.Background())
	ip, port := p.Backend()
	req, _ := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(ip, strconv.Itoa(port))+"/", nil)
	req.Host = "device.remote.example.com"
	req.Header.Set("Origin", "https://device.remote.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Upstream-Host") != "device.remote.example.com" {
		t.Fatalf("upstream Host = %q", resp.Header.Get("X-Upstream-Host"))
	}
	if resp.Header.Get("X-Upstream-Origin") != "http://device.remote.example.com" {
		t.Fatalf("upstream Origin = %q", resp.Header.Get("X-Upstream-Origin"))
	}
	if resp.Header.Get("X-Upstream-Forwarded-Host") != "device.remote.example.com" {
		t.Fatalf("upstream X-Forwarded-Host = %q", resp.Header.Get("X-Upstream-Forwarded-Host"))
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != harnessapi.SessionCookieName {
		t.Fatalf("unexpected cookies: %#v", cookies)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected cookie attributes: %#v", cookies[0])
	}
	if issuer.issued != 0 {
		t.Fatalf("IssueRemoteSession called %d times, want 0", issuer.issued)
	}
}

func TestProxyKeepsValidExistingSession(t *testing.T) {
	upstream := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie(harnessapi.SessionCookieName)
		_, _ = io.WriteString(w, cookie.Value)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()

	issuer := &fakeIssuer{}
	p, err := Start("http://"+listener.Addr().String(), "", "http://public.remote.test", issuer, testSession("initial-session"))
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer p.Close(context.Background())
	ip, port := p.Backend()
	req, _ := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(ip, strconv.Itoa(port))+"/", nil)
	req.AddCookie(&http.Cookie{Name: harnessapi.SessionCookieName, Value: "valid-session"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request returned error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "valid-session" {
		t.Fatalf("upstream cookie = %q, want valid-session", string(body))
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("valid session was replaced: %s", resp.Header.Get("Set-Cookie"))
	}
}

func TestProxyReplacesInvalidExistingSession(t *testing.T) {
	upstream := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie(harnessapi.SessionCookieName)
		_, _ = io.WriteString(w, cookie.Value)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()

	issuer := &fakeIssuer{}
	p, err := Start("http://"+listener.Addr().String(), "", "http://public.remote.test", issuer, testSession("initial-session"))
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer p.Close(context.Background())
	ip, port := p.Backend()
	req, _ := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(ip, strconv.Itoa(port))+"/", nil)
	req.AddCookie(&http.Cookie{Name: harnessapi.SessionCookieName, Value: "invalid-session"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request returned error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "initial-session" {
		t.Fatalf("upstream cookie = %q, want initial-session", string(body))
	}
	if resp.Header.Get("Set-Cookie") == "" {
		t.Fatal("invalid session was not replaced in the browser response")
	}
}

func TestProxyDoesNotUseExpiredInitialSession(t *testing.T) {
	upstream := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie(harnessapi.SessionCookieName)
		_, _ = io.WriteString(w, cookie.Value)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()

	issuer := &fakeIssuer{}
	expired := testSession("expired-initial-session")
	expired.ExpiresAt = time.Now().Add(-time.Second)
	p, err := Start("http://"+listener.Addr().String(), "", "http://public.remote.test", issuer, expired)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer p.Close(context.Background())
	ip, port := p.Backend()
	resp, err := http.Get("http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/")
	if err != nil {
		t.Fatalf("proxy request returned error: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "issued-session" {
		t.Fatalf("upstream cookie = %q, want issued-session", string(body))
	}
	if issuer.issued != 1 {
		t.Fatalf("IssueRemoteSession called %d times, want 1", issuer.issued)
	}
}

func testSession(token string) harnessapi.RemoteSession {
	return harnessapi.RemoteSession{
		CookieName:    harnessapi.SessionCookieName,
		SessionToken:  token,
		ExpiresAt:     time.Now().Add(time.Hour),
		MaxAgeSeconds: 3600,
	}
}
