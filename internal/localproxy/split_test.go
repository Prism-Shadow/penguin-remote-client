package localproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSplitFrontendAndBackendRouting(t *testing.T) {
	const public = "https://public.remote.test"
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			var frontendHost string
			handler := func(name string) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					wantHost := "public.remote.test"
					if name == "web" {
						wantHost = frontendHost
					}
					if r.Host != wantHost || r.Header.Get("Origin") != "http://"+wantHost || r.Header.Get("X-Forwarded-Proto") != "http" {
						http.Error(w, "incorrect target origin", 403)
						return
					}
					if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
						conn, rw, err := http.NewResponseController(w).Hijack()
						if err != nil {
							return
						}
						defer conn.Close()
						fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
						rw.Flush()
						io.Copy(conn, rw)
						return
					}
					w.Header().Set("X-Test-Target", name)
					io.WriteString(w, "ok")
				}
			}
			web := httptest.NewServer(handler("web"))
			defer web.Close()
			api := httptest.NewServer(handler("api"))
			defer api.Close()
			u, _ := url.Parse(web.URL)
			frontendHost = u.Host
			var p *Proxy
			var err error
			if manual {
				p, err = StartManual(web.URL, api.URL, public)
			} else {
				p, err = Start(web.URL, api.URL, public, &fakeIssuer{}, testSession("initial"))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			ip, port := p.Backend()
			address := net.JoinHostPort(ip, strconv.Itoa(port))
			for _, tc := range []struct{ path, target string }{
				{"/", "web"}, {"/@vite/client", "web"}, {"/src/main.tsx", "web"},
				{"/api/me", "api"}, {"/api", "api"}, {"/server/machine/api/sessions", "api"},
				{"/server/machine/ui", "web"}, {"/preview/token/file", "api"},
			} {
				req, _ := http.NewRequest("GET", "http://"+address+tc.path, nil)
				req.Header.Set("Origin", public)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 200 || resp.Header.Get("X-Test-Target") != tc.target {
					t.Fatalf("path=%s status=%d target=%s", tc.path, resp.StatusCode, resp.Header.Get("X-Test-Target"))
				}
			}
			// The API socket reaches Harness; the HMR socket reaches Vite.
			for _, path := range []string{"/api/socket", "/api/terminals/terminal-1/stream", "/?token=dev-hmr"} {
				conn, err := net.DialTimeout("tcp", address, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: public.remote.test\r\nOrigin: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", path, public)
				reader := bufio.NewReader(conn)
				response, err := http.ReadResponse(reader, nil)
				if err != nil || response.StatusCode != 101 {
					conn.Close()
					t.Fatalf("upgrade=%s error=%v", path, err)
				}
				conn.Write([]byte("echo"))
				data := make([]byte, 4)
				_, err = io.ReadFull(reader, data)
				conn.Close()
				if err != nil || string(data) != "echo" {
					t.Fatalf("upgrade echo=%s error=%v", path, err)
				}
			}
		})
	}
}
