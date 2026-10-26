package localproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtensionOriginBoundary(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, tc := range []struct {
			name, method, path string
			origins            []string
			headers            http.Header
			allowed            bool
		}{
			{"pair", "POST", extensionPairPath, []string{harnessExtensionOrigin}, nil, true},
			{"pair query", "POST", extensionPairPath + "?test=1", []string{harnessExtensionOrigin}, nil, true},
			{"preflight", "OPTIONS", extensionPairPath, []string{harnessExtensionOrigin}, http.Header{"Access-Control-Request-Method": {"POST"}}, true},
			{"upgrade", "GET", extensionWSPath, []string{harnessExtensionOrigin}, http.Header{"Connection": {"keep-alive, Upgrade"}, "Upgrade": {"websocket"}}, true},
			{"tool pair", "POST", extensionPairPath, nil, nil, true},
			{"tool ws", "GET", extensionWSPath, nil, nil, true},
			{"other extension", "POST", extensionPairPath, []string{"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, nil, false},
			{"other extension ws", "GET", extensionWSPath, []string{"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}}, false},
			{"evil web ws", "GET", extensionWSPath, []string{"https://evil.test"}, http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}}, false},
			{"duplicate ws origin", "GET", extensionWSPath, []string{harnessExtensionOrigin, harnessExtensionOrigin}, http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}}, false},
			{"evil web", "POST", extensionPairPath, []string{"https://evil.test"}, nil, false},
			{"empty", "POST", extensionPairPath, []string{""}, nil, false},
			{"opaque", "POST", extensionPairPath, []string{"null"}, nil, false},
			{"duplicate", "POST", extensionPairPath, []string{harnessExtensionOrigin, harnessExtensionOrigin}, nil, false},
			{"mixed duplicate", "POST", extensionPairPath, []string{harnessExtensionOrigin, "https://public.remote.test"}, nil, false},
			{"comma list", "POST", extensionPairPath, []string{harnessExtensionOrigin + "," + harnessExtensionOrigin}, nil, false},
			{"space list", "POST", extensionPairPath, []string{harnessExtensionOrigin + " https://evil.test"}, nil, false},
			{"origin slash", "POST", extensionPairPath, []string{harnessExtensionOrigin + "/"}, nil, false},
			{"origin path", "POST", extensionPairPath, []string{harnessExtensionOrigin + "/page.html"}, nil, false},
			{"origin query", "POST", extensionPairPath, []string{harnessExtensionOrigin + "?"}, nil, false},
			{"origin fragment", "POST", extensionPairPath, []string{harnessExtensionOrigin + "#"}, nil, false},
			{"origin port", "POST", extensionPairPath, []string{harnessExtensionOrigin + ":80"}, nil, false},
			{"origin userinfo", "POST", extensionPairPath, []string{"chrome-extension://evil@dodgfhpcbmkjfcbgnoidablfgjjhhmgp"}, nil, false},
			{"origin suffix", "POST", extensionPairPath, []string{harnessExtensionOrigin + ".evil.test"}, nil, false},
			{"origin case", "POST", extensionPairPath, []string{strings.ToUpper(harnessExtensionOrigin)}, nil, false},
			{"origin escaped", "POST", extensionPairPath, []string{strings.Replace(harnessExtensionOrigin, "dodg", "%64odg", 1)}, nil, false},
			{"origin backslash", "POST", extensionPairPath, []string{harnessExtensionOrigin + "\\"}, nil, false},
			{"pair GET", "GET", extensionPairPath, []string{harnessExtensionOrigin}, nil, false},
			{"pair PUT", "PUT", extensionPairPath, []string{harnessExtensionOrigin}, nil, false},
			{"pair DELETE", "DELETE", extensionPairPath, []string{harnessExtensionOrigin}, nil, false},
			{"pair HEAD", "HEAD", extensionPairPath, []string{harnessExtensionOrigin}, nil, false},
			{"ws POST", "POST", extensionWSPath, []string{harnessExtensionOrigin}, nil, false},
			{"ws OPTIONS", "OPTIONS", extensionWSPath, []string{harnessExtensionOrigin}, nil, false},
			{"ws no upgrade", "GET", extensionWSPath, []string{harnessExtensionOrigin}, nil, false},
			{"ws missing connection", "GET", extensionWSPath, []string{harnessExtensionOrigin}, http.Header{"Upgrade": {"websocket"}}, false},
			{"preflight missing method", "OPTIONS", extensionPairPath, []string{harnessExtensionOrigin}, nil, false},
			{"preflight wrong method", "OPTIONS", extensionPairPath, []string{harnessExtensionOrigin}, http.Header{"Access-Control-Request-Method": {"DELETE"}}, false},
			{"preflight duplicate method", "OPTIONS", extensionPairPath, []string{harnessExtensionOrigin}, http.Header{"Access-Control-Request-Method": {"POST", "POST"}}, false},
			{"pairings admin route", "POST", extensionPairPath + "ings", []string{harnessExtensionOrigin}, nil, false},
			{"revoke admin route", "DELETE", "/api/builtin-browser/extension/id", []string{harnessExtensionOrigin}, nil, false},
			{"ordinary write", "POST", "/api/resource", []string{harnessExtensionOrigin}, nil, false},
			{"terminal", "GET", "/api/terminals/id/stream", []string{harnessExtensionOrigin}, http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}}, false},
			{"vite", "GET", "/@vite/client", []string{harnessExtensionOrigin}, nil, false},
			{"trailing slash", "POST", extensionPairPath + "/", []string{harnessExtensionOrigin}, nil, false},
			{"path child", "POST", extensionPairPath + "/child", []string{harnessExtensionOrigin}, nil, false},
			{"encoded path", "POST", strings.Replace(extensionPairPath, "pair", "%70air", 1), []string{harnessExtensionOrigin}, nil, false},
			{"encoded slash", "POST", strings.Replace(extensionPairPath, "/extension/", "/extension%2f", 1), []string{harnessExtensionOrigin}, nil, false},
			{"double slash", "POST", "/api//builtin-browser/extension/pair", []string{harnessExtensionOrigin}, nil, false},
			{"dot path", "POST", "/api/./builtin-browser/extension/pair", []string{harnessExtensionOrigin}, nil, false},
			{"machine prefix", "POST", "/server/id" + extensionPairPath, []string{harnessExtensionOrigin}, nil, false},
			{"erase origin", "POST", extensionPairPath, []string{harnessExtensionOrigin}, http.Header{"Connection": {"Origin"}}, false},
			{"erase protocols", "GET", extensionWSPath, []string{harnessExtensionOrigin}, http.Header{"Connection": {"Upgrade, Sec-WebSocket-Protocol"}, "Upgrade": {"websocket"}}, false},
		} {
			t.Run(fmt.Sprintf("manual=%t/%s", manual, tc.name), func(t *testing.T) {
				var hits atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					if !reflect.DeepEqual(r.Header.Values("Origin"), tc.origins) || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Error("extension origin changed or ambient credential forwarded")
					}
					w.WriteHeader(204)
				}))
				defer backend.Close()
				issuer := &boundaryIssuer{}
				proxyURL := boundaryProxy(t, manual, backend.URL, "https://public.remote.test", issuer)
				req, _ := http.NewRequest(tc.method, proxyURL+tc.path, nil)
				for key, values := range tc.headers {
					req.Header[key] = values
				}
				req.Header["Origin"] = tc.origins
				req.Header.Set("Cookie", "penguin_session=ambient-admin")
				req.Header.Set("Authorization", "Bearer ambient-admin")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				want, wantHits := 403, int32(0)
				if tc.allowed {
					want, wantHits = 204, 1
				}
				if resp.StatusCode != want || hits.Load() != wantHits || issuer.calls.Load() != 0 || len(resp.Cookies()) != 0 {
					t.Fatalf("status=%d hits=%d issuer=%d cookies=%d; want %d/%d/0/0", resp.StatusCode, hits.Load(), issuer.calls.Load(), len(resp.Cookies()), want, wantHits)
				}
			})
		}
	}
}

func extensionProxy(t *testing.T, manual bool, target, api string, issuer SessionIssuer) (string, *Proxy) {
	t.Helper()
	var p *Proxy
	var err error
	if manual {
		p, err = StartManual(target, api, "https://public.remote.test")
	} else {
		p, err = Start(target, api, "https://public.remote.test", issuer, testSession("initial-session"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	ip, port := p.Backend()
	return fmt.Sprintf("http://%s:%d", ip, port), p
}

// This fixture models Harness's code-only route, including its CORS/PNA and
// invalid-code responses. It does not stand in for Gateway authorization.
func TestExtensionPairingAndPreflight(t *testing.T) {
	const body = `{"code":"one-time-code","name":"Chrome","version":"0.2.17"}`
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("extension request reached Vite")
				w.WriteHeader(500)
			}))
			defer web.Close()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "public.remote.test" || r.Header.Get("X-Forwarded-Host") != r.Host || r.Header.Get("X-Forwarded-Proto") != "http" || r.Header.Get("Forwarded") != "" {
					t.Error("extension changed Harness routing/authority")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
					t.Error("extension borrowed a browser credential")
				}
				origin := r.Header.Get("Origin")
				if origin != "" && origin != harnessExtensionOrigin {
					http.Error(w, "forbidden_origin", 403)
					return
				}
				if origin != "" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
				}
				if r.Method == "OPTIONS" {
					if r.Header.Get("Access-Control-Request-Method") != "POST" || r.Header.Get("Access-Control-Request-Headers") != "content-type" {
						t.Error("preflight request changed")
					}
					w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "content-type")
					w.Header().Set("Access-Control-Max-Age", "600")
					if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
						w.Header().Set("Access-Control-Allow-Private-Network", "true")
					}
					w.WriteHeader(204)
					return
				}
				data, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || string(data) != body {
					t.Error("pairing request changed")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.RawQuery == "invalid=1" {
					w.WriteHeader(401)
					io.WriteString(w, `{"error":{"code":"invalid_code"}}`)
					return
				}
				io.WriteString(w, `{"token":"extension-token"}`)
			}))
			defer api.Close()
			issuer := &boundaryIssuer{}
			proxyURL, p := extensionProxy(t, manual, web.URL, api.URL, issuer)
			for _, tc := range []struct {
				name, method, query, origin, pna string
				status                           int
			}{
				{"cors", "OPTIONS", "", harnessExtensionOrigin, "", 204},
				{"pna", "OPTIONS", "", harnessExtensionOrigin, "true", 204},
				{"pair", "POST", "", harnessExtensionOrigin, "", 200},
				{"bad code", "POST", "?invalid=1", harnessExtensionOrigin, "", 401},
				{"public page", "POST", "", "https://public.remote.test", "", 403},
				{"tool bad code", "POST", "?invalid=1", "", "", 401},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req, _ := http.NewRequest(tc.method, proxyURL+extensionPairPath+tc.query, strings.NewReader(body))
					if tc.origin != "" {
						req.Header.Set("Origin", tc.origin)
						if tc.method == "OPTIONS" {
							req.Header.Set("Access-Control-Request-Method", "POST")
							req.Header.Set("Access-Control-Request-Headers", "content-type")
						}
					}
					req.Header.Set("Access-Control-Request-Private-Network", tc.pna)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Cookie", "penguin_session=ambient-admin")
					req.Header.Set("Authorization", "Bearer ambient-admin")
					req.Header.Set("Forwarded", "host=evil.test;proto=https")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					data, _ := io.ReadAll(resp.Body)
					if resp.StatusCode != tc.status || len(resp.Cookies()) != 0 || issuer.calls.Load() != 0 {
						t.Fatalf("status=%d cookies=%d issuer=%d", resp.StatusCode, len(resp.Cookies()), issuer.calls.Load())
					}
					if tc.status == 401 && string(data) != `{"error":{"code":"invalid_code"}}` {
						t.Fatalf("Harness denial changed: %s", data)
					}
					if tc.origin == harnessExtensionOrigin {
						if resp.Header.Get("Access-Control-Allow-Origin") != harnessExtensionOrigin || resp.Header.Get("Vary") != "Origin" || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
							t.Error("Harness CORS response changed")
						}
						if tc.method == "OPTIONS" && (resp.Header.Get("Access-Control-Allow-Methods") != "POST, OPTIONS" || resp.Header.Get("Access-Control-Allow-Headers") != "content-type" || resp.Header.Get("Access-Control-Max-Age") != "600" || resp.Header.Get("Access-Control-Allow-Private-Network") != tc.pna) {
							t.Error("Harness preflight response changed")
						}
					}
				})
			}
			if !manual {
				p.mu.Lock()
				pending := p.pending
				p.mu.Unlock()
				if pending == nil || pending.SessionToken != "initial-session" {
					t.Fatal("extension consumed the browser's initial session")
				}
			}
		})
	}
}

func TestExtensionWebSocketProtocolAndRevocation(t *testing.T) {
	for _, manual := range []bool{false, true} {
		for _, tc := range []struct {
			name, origin string
			protocols    []string
			status       int
			closeCode    uint16
		}{
			{"valid", harnessExtensionOrigin, []string{"penguin-browser.1, token.abcdefghijklmnop"}, 101, 4003},
			{"multiple protocol header values", harnessExtensionOrigin, []string{"penguin-browser.1", "token.abcdefghijklmnop"}, 101, 4003},
			{"already revoked", harnessExtensionOrigin, []string{"penguin-browser.1, token.revoked_token_123"}, 101, 4003},
			{"disabled", harnessExtensionOrigin, []string{"penguin-browser.1, token.disabled_token_123"}, 101, 4009},
			{"missing protocol", harnessExtensionOrigin, []string{"token.abcdefghijklmnop"}, 101, 4005},
			{"unknown token", harnessExtensionOrigin, []string{"penguin-browser.1, token.unknown_token_123"}, 401, 0},
			{"missing token", harnessExtensionOrigin, []string{"penguin-browser.1"}, 401, 0},
			{"tool missing token", "", []string{"penguin-browser.1"}, 401, 0},
			{"public page with token", "https://public.remote.test", []string{"penguin-browser.1, token.abcdefghijklmnop"}, 403, 0},
		} {
			t.Run(fmt.Sprintf("manual=%t/%s", manual, tc.name), func(t *testing.T) {
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Origin") != tc.origin || !reflect.DeepEqual(r.Header.Values("Sec-WebSocket-Protocol"), tc.protocols) || r.URL.RequestURI() != extensionWSPath || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Error("extension handshake changed or borrowed a session")
					}
					if tc.status != 101 {
						w.WriteHeader(tc.status)
						return
					}
					conn, rw, err := http.NewResponseController(w).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(3 * time.Second))
					sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
					fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n", base64.StdEncoding.EncodeToString(sum[:]))
					if tc.closeCode != 4005 {
						fmt.Fprint(rw, "Sec-WebSocket-Protocol: penguin-browser.1\r\n")
					}
					fmt.Fprint(rw, "\r\n")
					rw.Flush()
					if tc.name == "valid" {
						// A masked client text frame and an unmasked server echo.
						frame := []byte{0x81, 0x84, 1, 2, 3, 4, 'e' ^ 1, 'c' ^ 2, 'h' ^ 3, 'o' ^ 4}
						got := make([]byte, len(frame))
						if _, err := io.ReadFull(rw, got); err != nil || !bytes.Equal(got, frame) {
							t.Errorf("client frame changed: %x %v", got, err)
							return
						}
						conn.Write([]byte{0x81, 4, 'e', 'c', 'h', 'o'})
					}
					// Server close frames must pass unchanged, including revocation.
					conn.Write([]byte{0x88, 2, byte(tc.closeCode >> 8), byte(tc.closeCode)})
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
				fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: public.remote.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nCookie: penguin_session=ambient-admin\r\nAuthorization: Bearer ambient-admin\r\n", extensionWSPath)
				if tc.origin != "" {
					fmt.Fprintf(conn, "Origin: %s\r\n", tc.origin)
				}
				for _, protocol := range tc.protocols {
					fmt.Fprintf(conn, "Sec-WebSocket-Protocol: %s\r\n", protocol)
				}
				fmt.Fprint(conn, "\r\n")
				reader := bufio.NewReader(conn)
				resp, err := http.ReadResponse(reader, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != tc.status || len(resp.Cookies()) != 0 || issuer.calls.Load() != 0 {
					t.Fatalf("status=%d cookies=%d issuer=%d", resp.StatusCode, len(resp.Cookies()), issuer.calls.Load())
				}
				if tc.status != 101 {
					return
				}
				wantProtocol := "penguin-browser.1"
				if tc.closeCode == 4005 {
					wantProtocol = ""
				}
				if resp.Header.Get("Sec-WebSocket-Protocol") != wantProtocol {
					t.Fatal("selected subprotocol changed")
				}
				if tc.name == "valid" {
					conn.Write([]byte{0x81, 0x84, 1, 2, 3, 4, 'e' ^ 1, 'c' ^ 2, 'h' ^ 3, 'o' ^ 4})
					echo := make([]byte, 6)
					if _, err := io.ReadFull(reader, echo); err != nil || !bytes.Equal(echo, []byte{0x81, 4, 'e', 'c', 'h', 'o'}) {
						t.Fatalf("server frame changed: %x %v", echo, err)
					}
				}
				closeFrame := make([]byte, 4)
				if _, err := io.ReadFull(reader, closeFrame); err != nil || closeFrame[0] != 0x88 || closeFrame[1] != 2 || binary.BigEndian.Uint16(closeFrame[2:]) != tc.closeCode {
					t.Fatalf("close frame changed: %x %v", closeFrame, err)
				}
			})
		}
	}
}
