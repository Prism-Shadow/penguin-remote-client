package localproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
)

type boundaryIssuer struct{ calls atomic.Int32 }

func (i *boundaryIssuer) IssueRemoteSession(context.Context) (harnessapi.RemoteSession, error) {
	i.calls.Add(1)
	return testSession("boundary-session"), nil
}

func (i *boundaryIssuer) ValidateSession(context.Context, string) (bool, error) {
	i.calls.Add(1)
	return true, nil
}

func boundaryProxy(t *testing.T, manual bool, target, public string, issuer SessionIssuer) string {
	t.Helper()
	var p *Proxy
	var err error
	if manual {
		p, err = StartManual(target, "", public)
	} else {
		p, err = Start(target, "", public, issuer, testSession("boundary-session"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	ip, port := p.Backend()
	return fmt.Sprintf("http://%s:%d", ip, port)
}

func TestProxyOriginBoundaryBeforeCredentials(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			origins []string
			allowed bool
		}{
			{"exact", []string{"https://public.remote.test"}, true},
			{"default port and DNS case", []string{"https://PUBLIC.REMOTE.TEST:443"}, true},
			{"absent", nil, true},
			{"other user", []string{"https://other.remote.test"}, false},
			{"wrong scheme", []string{"http://public.remote.test"}, false},
			{"wrong port", []string{"https://public.remote.test:444"}, false},
			{"suffix", []string{"https://public.remote.test.evil.test"}, false},
			{"subdomain", []string{"https://other.public.remote.test"}, false},
			{"local origin", []string{"http://localhost:7364"}, false},
			{"opaque", []string{"null"}, false},
			{"empty", []string{""}, false},
			{"credentials", []string{"https://other@public.remote.test"}, false},
			{"path", []string{"https://public.remote.test/path"}, false},
			{"query", []string{"https://public.remote.test?"}, false},
			{"fragment", []string{"https://public.remote.test#"}, false},
			{"list", []string{"https://public.remote.test https://other.remote.test"}, false},
			{"comma list", []string{"https://public.remote.test,https://other.remote.test"}, false},
			{"duplicate", []string{"https://public.remote.test", "https://public.remote.test"}, false},
		} {
			t.Run(fmt.Sprintf("manual=%t/%s", manual, tc.name), func(t *testing.T) {
				var forwarded atomic.Int32
				var targetHost string
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded.Add(1)
					if r.Host != targetHost || r.Header.Get("X-Forwarded-Host") != targetHost || r.Header.Get("X-Forwarded-Proto") != "http" || r.Header.Get("Forwarded") != "" {
						t.Error("local authority or trusted forwarding headers are inconsistent")
					}
					wantOrigin := ""
					if len(tc.origins) > 0 {
						wantOrigin = "http://" + targetHost
					}
					if r.Header.Get("Origin") != wantOrigin || r.Header.Get("Authorization") != "" {
						t.Error("unexpected origin or local API credential")
					}
					if manual && r.Header.Get("Cookie") != "preferences=dark" {
						t.Error("manual mode injected a credential")
					}
					if !manual {
						cookie, err := r.Cookie(harnessapi.SessionCookieName)
						if err != nil || cookie.Value != "boundary-session" {
							t.Error("automatic mode lost the exchanged session")
						}
					}
					body, _ := io.ReadAll(r.Body)
					if r.Method != "POST" || r.URL.RequestURI() != "/api/resource?test=1" || string(body) != `{"value":1}` {
						t.Error("forwarded request changed")
					}
					io.WriteString(w, "ok")
				}))
				defer backend.Close()
				targetHost = "public.remote.test"
				issuer := &boundaryIssuer{}
				proxyURL := boundaryProxy(t, manual, backend.URL, "https://public.remote.test/", issuer)
				req, _ := http.NewRequest("POST", proxyURL+"/api/resource?test=1", strings.NewReader(`{"value":1}`))
				req.Host = "untrusted-forwarded-host.test"
				req.Header["Origin"] = tc.origins
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Forwarded-Host", "spoofed.test")
				req.Header.Set("X-Forwarded-Proto", "https")
				req.Header.Set("Forwarded", "host=spoofed.test;proto=https")
				req.Header.Set("Cookie", "preferences=dark")
				if !tc.allowed {
					req.AddCookie(&http.Cookie{Name: harnessapi.SessionCookieName, Value: "untrusted-session"})
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				wantStatus := http.StatusForbidden
				if tc.allowed {
					wantStatus = http.StatusOK
				}
				if resp.StatusCode != wantStatus {
					t.Fatalf("status=%d want=%d", resp.StatusCode, wantStatus)
				}
				if !tc.allowed && (forwarded.Load() != 0 || issuer.calls.Load() != 0 || len(resp.Cookies()) != 0) {
					t.Fatal("rejected origin reached Harness or touched local credentials")
				}
			})
		}
	}
}

func TestProxyWebSocketOriginTranslation(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, allowed := range []bool{false, true} {
			t.Run(fmt.Sprintf("manual=%t/allowed=%t", manual, allowed), func(t *testing.T) {
				var forwarded atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					forwarded.Add(1)
					if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("X-Forwarded-Proto") != "http" {
						http.Error(w, "cross_origin_write", 403)
						return
					}
					conn, rw, err := http.NewResponseController(w).Hijack()
					if err != nil {
						return
					}
					defer conn.Close()
					fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
					rw.Flush()
					io.Copy(conn, rw)
				}))
				defer backend.Close()
				issuer := &boundaryIssuer{}
				proxyURL := boundaryProxy(t, manual, backend.URL, "https://public.remote.test", issuer)
				conn, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyURL, "http://"), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				requestOrigin := "https://other.remote.test"
				if allowed {
					requestOrigin = "https://public.remote.test"
				}
				fmt.Fprintf(conn, "GET /api/socket HTTP/1.1\r\nHost: spoofed.test\r\nOrigin: %s\r\nX-Forwarded-Proto: https\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", requestOrigin)
				reader := bufio.NewReader(conn)
				resp, err := http.ReadResponse(reader, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !allowed {
					defer resp.Body.Close()
					if resp.StatusCode != 403 || forwarded.Load() != 0 || issuer.calls.Load() != 0 || len(resp.Cookies()) != 0 {
						t.Fatal("cross-origin upgrade was not rejected before authentication")
					}
					return
				}
				if resp.StatusCode != 101 {
					t.Fatalf("upgrade status=%d", resp.StatusCode)
				}
				conn.Write([]byte("echo"))
				data := make([]byte, 4)
				if _, err := io.ReadFull(reader, data); err != nil || string(data) != "echo" {
					t.Fatalf("WebSocket duplex forwarding failed: %v", err)
				}
			})
		}
	}
}

func TestProxyRequiresConfiguredPublicOrigin(t *testing.T) {
	for _, public := range []string{"", "//remote.test", "ftp://remote.test", "https://user@remote.test", "https://remote.test/path", "https://remote.test?query=1", "https://remote.test#fragment", "https://remote.test:65536"} {
		t.Run(public, func(t *testing.T) {
			p, err := StartManual("http://localhost:7364", "", public)
			if err == nil {
				p.Close(context.Background())
				t.Fatal("invalid public origin started a proxy")
			}
		})
	}
}

func TestNonDefaultPublicOriginPort(t *testing.T) {
	o, err := parseOrigin("http://public.remote.test:8080")
	if err != nil || !o.allows([]string{"http://public.remote.test:8080"}) || o.allows([]string{"http://public.remote.test"}) || o.allows([]string{"https://public.remote.test:8080"}) {
		t.Fatal("non-default public port was not matched exactly")
	}
}
