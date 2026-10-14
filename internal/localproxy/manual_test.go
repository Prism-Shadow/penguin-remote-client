package localproxy

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
	"github.com/Prism-Shadow/penguin-remote-client/internal/publicerror"
)

func manualTestProxy(t *testing.T, handler http.HandlerFunc) (string, *http.Client, *Proxy) {
	t.Helper()
	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)
	p, err := StartManual(backend.URL, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		p.Close(ctx)
	})
	ip, port := p.Backend()
	jar, _ := cookiejar.New(nil)
	return fmt.Sprintf("http://%s:%d", ip, port), &http.Client{Jar: jar, Timeout: 2 * time.Second}, p
}

func TestManualProxyBrowserLoginLifecycle(t *testing.T) {
	var probes atomic.Int32
	var loggedIn atomic.Bool
	publicOrigin := "https://public.remote.test"
	proxyURL, client, p := manualTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("local token injected")
		}
		if r.Host != "public.remote.test" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != publicOrigin) {
			t.Error("packaged Harness Host/Origin boundary changed")
		}
		cookie, _ := r.Cookie(harnessapi.SessionCookieName)
		valid := loggedIn.Load() && cookie != nil && cookie.Value == "browser-login-session"
		switch r.URL.Path {
		case "/api/remote/session", "/api/me":
			probes.Add(1)
			t.Error("manual proxy called exchange/validation endpoint")
			w.WriteHeader(500)
		case "/api/auth/login":
			body, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || string(body) != `{"username":"user","password":"password"}` {
				t.Error("login request changed")
			}
			if cookie != nil {
				t.Error("admin cookie injected before login")
			}
			loggedIn.Store(true)
			http.SetCookie(w, &http.Cookie{Name: harnessapi.SessionCookieName, Value: "browser-login-session", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.SetCookie(w, &http.Cookie{Name: "preferences", Value: "dark", Path: "/"})
			io.WriteString(w, "logged in")
		case "/api/auth/logout":
			if !valid {
				t.Error("logout lost browser session")
			}
			loggedIn.Store(false)
			http.SetCookie(w, &http.Cookie{Name: harnessapi.SessionCookieName, Value: "", Path: "/", MaxAge: -1})
			w.WriteHeader(204)
		case "/api/events":
			if !valid {
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: signed-in\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/ws":
			if !valid {
				w.WriteHeader(401)
				return
			}
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			rw.Flush()
			io.Copy(conn, rw)
		default:
			if !valid {
				w.WriteHeader(401)
				io.WriteString(w, "Harness login required")
				return
			}
			io.WriteString(w, "authenticated REST")
		}
	})
	if p.issuer != nil || p.mode != harnessapi.LoginManual {
		t.Fatal("manual mode retains an issuer")
	}
	do := func(path, method, body string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, proxyURL+path, strings.NewReader(body))
		r.Host = "public.remote.test"
		r.Header.Set("Origin", publicOrigin)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := do("/api/resource", "GET", "")
	if resp.StatusCode != 401 || len(resp.Cookies()) != 0 || resp.Header.Get(publicerror.Header) != "" {
		t.Fatal("unauthenticated Harness response altered")
	}
	resp.Body.Close()
	resp = do("/api/auth/login", "POST", `{"username":"user","password":"password"}`)
	if resp.StatusCode != 200 || len(resp.Cookies()) != 2 {
		t.Fatal("login cookies not preserved")
	}
	resp.Body.Close()
	resp = do("/api/resource", "GET", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "authenticated REST" || len(resp.Cookies()) != 0 {
		t.Fatal("REST did not use browser cookie")
	}
	resp = do("/api/events", "GET", "")
	if resp.StatusCode != 200 {
		t.Fatal("SSE did not use browser cookie")
	}
	first := make([]byte, len("data: signed-in\n\n"))
	if _, err := io.ReadFull(resp.Body, first); err != nil || string(first) != "data: signed-in\n\n" {
		t.Fatalf("SSE first event: %q %v", first, err)
	}
	resp.Body.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyURL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: public.remote.test\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: penguin_session=browser-login-session\r\n\r\n", publicOrigin)
	reader := bufio.NewReader(conn)
	upgrade, err := http.ReadResponse(reader, nil)
	if err != nil || upgrade.StatusCode != 101 {
		t.Fatalf("WebSocket cookie upgrade: %v", err)
	}
	conn.Write([]byte("browser-echo"))
	echo := make([]byte, len("browser-echo"))
	_, err = io.ReadFull(reader, echo)
	conn.Close()
	if err != nil || string(echo) != "browser-echo" {
		t.Fatal("WebSocket duplex forwarding failed")
	}
	resp = do("/api/auth/logout", "POST", "")
	if resp.StatusCode != 204 || len(resp.Cookies()) != 1 || resp.Cookies()[0].MaxAge != -1 {
		t.Fatal("logout cookie deletion changed")
	}
	resp.Body.Close()
	for _, path := range []string{"/api/resource", "/api/events", "/ws"} {
		resp = do(path, "GET", "")
		if resp.StatusCode != 401 || len(resp.Cookies()) != 0 {
			t.Errorf("logout silently reauthenticated %s", path)
		}
		resp.Body.Close()
	}
	if probes.Load() != 0 {
		t.Fatal("manual mode invoked session APIs")
	}
}

func TestManualProxyPreservesHarnessErrorsAndBrowserHeaders(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			proxyURL, client, _ := manualTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer browser-owned" || r.Header.Get("Cookie") != "penguin_session=browser-owned; other=preserved" {
					t.Error("browser credentials changed")
				}
				w.Header().Set(publicerror.Header, "harness_session_failed")
				w.Header().Add("Set-Cookie", "penguin_session=deleted; Max-Age=0; Path=/")
				w.WriteHeader(status)
				io.WriteString(w, "Harness application response")
			})
			r, _ := http.NewRequest("GET", proxyURL, nil)
			r.Header.Set("Authorization", "Bearer browser-owned")
			r.Header.Set("Cookie", "penguin_session=browser-owned; other=preserved")
			resp, err := client.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != status || string(body) != "Harness application response" || resp.Header.Get(publicerror.Header) != "" || len(resp.Cookies()) != 1 {
				t.Fatal("Harness error was rewritten")
			}
		})
	}
}

func TestAutomaticProxyDoesNotFallbackAfterUnsupportedError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("automatic failure forwarded unauthenticated request")
	}))
	defer backend.Close()
	p, err := Start(backend.URL, unsupportedIssuer{}, false, false, harnessapi.RemoteSession{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	ip, port := p.Backend()
	for _, cookie := range []string{"", "existing-browser-session"} {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s:%d", ip, port), nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: harnessapi.SessionCookieName, Value: cookie})
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 502 || resp.Header.Get(publicerror.Header) != "harness_session_failed" || p.mode != harnessapi.LoginAutomatic {
			t.Fatal("automatic mode silently degraded")
		}
	}
}

func TestManualProxyGzipAndCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	payload := `{"value":"` + strings.Repeat("manual-browser-data", 8000) + `"}`
	proxyURL, client, _ := manualTestProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "penguin_session=browser-owned" || r.Header.Get("Authorization") != "" {
			t.Error("manual credentials changed")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/cancel" {
			io.WriteString(w, `{"pending":`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(cancelled)
			return
		}
		io.WriteString(w, payload)
	})
	r, _ := http.NewRequest("GET", proxyURL, nil)
	r.Header.Set("Cookie", "penguin_session=browser-owned")
	r.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" || len(resp.Cookies()) != 0 {
		t.Fatal("manual JSON gzip or credentials changed")
	}
	reader, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	reader.Close()
	resp.Body.Close()
	if err != nil || string(body) != payload {
		t.Fatal("manual gzip body corrupted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, _ = http.NewRequestWithContext(ctx, "GET", proxyURL+"/cancel", nil)
	r.Header.Set("Cookie", "penguin_session=browser-owned")
	r.Header.Set("Accept-Encoding", "gzip")
	resp, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("manual buffered gzip cancellation did not reach Harness")
	}
}

type unsupportedIssuer struct{}

func (unsupportedIssuer) IssueRemoteSession(context.Context) (harnessapi.RemoteSession, error) {
	return harnessapi.RemoteSession{}, &harnessapi.SessionUnsupportedError{StatusCode: 404}
}
func (unsupportedIssuer) ValidateSession(context.Context, string) (bool, error) {
	return false, &harnessapi.SessionUnsupportedError{StatusCode: 404}
}
