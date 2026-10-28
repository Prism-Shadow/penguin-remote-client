package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
)

func runtimeConfig(t *testing.T, localURL string) remote.Config {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "api-token"), []byte("local-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return remote.Config{Source: remote.SourceDevConfig, Device: remote.DeviceConfig{ID: "fallback-test"}, Harness: remote.HarnessConfig{LocalURL: localURL, DataRoot: root}, Tunnel: &remote.TunnelConfig{ServerAddr: "127.0.0.1", ServerPort: 7000, Token: "test-token", ProxyName: "slot", CustomDomain: "public.remote.test", PublicURL: "https://public.remote.test"}}
}

func TestRuntimeRecoversHarnessTokenRotationWithoutReconnecting(t *testing.T) {
	for _, scenario := range []string{"rotated", "missing bound file", "wrong bound token", "manual"} {
		t.Run(scenario, func(t *testing.T) {
			var boot atomic.Int32
			var issued, validations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				current := "local-token"
				if boot.Load() > 0 {
					current = "rotated-token"
				}
				switch r.URL.Path {
				case "/":
					fmt.Fprint(w, "<title>PenguinHarness</title>")
				case "/api/me":
					if r.Header.Get("Authorization") == "Bearer "+current {
						validations.Add(1)
						fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"token"}`)
					} else if cookie, err := r.Cookie("penguin_session"); err == nil && cookie.Value == "runtime-session" {
						fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"remote"}`)
					} else {
						w.WriteHeader(401)
					}
				case "/api/remote/session":
					issued.Add(1)
					if scenario == "manual" {
						w.WriteHeader(404)
						fmt.Fprint(w, `{}`)
					} else if r.Header.Get("Authorization") != "Bearer "+current {
						w.WriteHeader(401)
					} else {
						json.NewEncoder(w).Encode(map[string]any{"cookieName": "penguin_session", "sessionToken": "runtime-session", "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339), "maxAgeSeconds": 3600})
					}
				case "/api/probe":
					if r.Header.Get("Authorization") != "" {
						t.Error("local API token leaked into browser forwarding")
					}
					fmt.Fprint(w, `{"ok":true}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			cfg := runtimeConfig(t, server.URL)
			rt, err := newClientRuntime(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			originalRunner := rt.runner
			ip, port := rt.proxy.Backend()
			request := func(cookieValue string, want int) {
				t.Helper()
				req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s:%d/api/probe", ip, port), nil)
				req.Host = "public.remote.test"
				// Empty matches clearing just the cookie's value in browser tools.
				req.AddCookie(&http.Cookie{Name: "penguin_session", Value: cookieValue})
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != want {
					t.Fatalf("got %d want %d: %s", resp.StatusCode, want, body)
				}
				if want == 502 && !strings.Contains(string(body), "暂时无法连接 Harness") {
					t.Fatal("session failure was not preserved")
				}
				if want == 200 && scenario != "manual" && cookieValue == "" && len(resp.Cookies()) != 1 {
					t.Fatal("new browser session was not set")
				}
				if scenario == "manual" && len(resp.Cookies()) != 0 {
					t.Fatal("manual mode injected a session")
				}
			}
			request("", 200) // Consume the startup session before restarting Harness.
			boot.Store(1)
			tokenPath := filepath.Join(cfg.Harness.DataRoot, "api-token")
			if scenario == "missing bound file" {
				if err := os.Remove(tokenPath); err != nil {
					t.Fatal(err)
				}
				// Another installation must not rescue a missing bound credential.
				other := t.TempDir()
				if err := os.WriteFile(filepath.Join(other, "api-token"), []byte("rotated-token"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PENGUIN_HOME", other)
			} else {
				token := "rotated-token"
				if scenario == "wrong bound token" {
					token = "unrelated-token"
				}
				if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
					t.Fatal(err)
				}
			}
			request("runtime-session", 200) // A valid browser session still survives.
			want := 200
			if scenario == "missing bound file" || scenario == "wrong bound token" {
				want = 502
			}
			request("", want)
			request("", want)
			if rt.runner != originalRunner || rt.DataRoot() != cfg.Harness.DataRoot {
				t.Fatal("credential refresh replaced the tunnel or installation binding")
			}
			if scenario == "rotated" && (validations.Load() != 2 || issued.Load() != 4) {
				t.Fatalf("validations=%d session calls=%d", validations.Load(), issued.Load())
			}
			if scenario == "manual" && (validations.Load() != 1 || issued.Load() != 1) {
				t.Fatal("manual mode attempted automatic credential recovery")
			}
		})
	}
}

func TestRuntimeTranslatesOnlyGrantedPublicOrigin(t *testing.T) {
	for _, sessionStatus := range []int{200, 404} {
		t.Run(fmt.Sprint(sessionStatus), func(t *testing.T) {
			var forwarded atomic.Int32
			var localHost string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/":
					fmt.Fprint(w, "<title>PenguinHarness</title>")
				case "/api/me":
					fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"token"}`)
				case "/api/remote/session":
					w.WriteHeader(sessionStatus)
					if sessionStatus == 200 {
						json.NewEncoder(w).Encode(map[string]any{"cookieName": "penguin_session", "sessionToken": "runtime-session", "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339), "maxAgeSeconds": 3600})
					} else {
						fmt.Fprint(w, `{"error":{"code":"not_found"}}`)
					}
				case "/api/probe":
					forwarded.Add(1)
					if r.Host != localHost || r.Header.Get("Origin") != "http://"+localHost || r.Header.Get("X-Forwarded-Proto") != "http" {
						t.Error("runtime did not translate the public origin to the selected Harness")
					}
					fmt.Fprint(w, `{}`)
				default:
					t.Error("unexpected request")
				}
			}))
			defer server.Close()
			localHost = "public.remote.test"
			rt, err := newClientRuntime(context.Background(), runtimeConfig(t, server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			ip, port := rt.proxy.Backend()
			for _, origin := range []string{"https://other.remote.test", "https://public.remote.test"} {
				req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s:%d/api/probe", ip, port), nil)
				req.Host = "other.remote.test"
				req.Header.Set("Origin", origin)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				want := 403
				if origin == "https://public.remote.test" {
					want = 200
				}
				if resp.StatusCode != want {
					t.Fatalf("status=%d want=%d", resp.StatusCode, want)
				}
			}
			if forwarded.Load() != 1 {
				t.Fatal("untrusted origin reached Harness")
			}
		})
	}
}

func TestRuntimeLoginNegotiationAndRecovery(t *testing.T) {
	for _, initialStatus := range []int{200, 404, 405} {
		t.Run(fmt.Sprint(initialStatus), func(t *testing.T) {
			var sessionStatus, probes, validations atomic.Int32
			sessionStatus.Store(int32(initialStatus))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer local-token" {
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/api/me":
					validations.Add(1)
					fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"token"}`)
				case "/api/remote/session":
					if validations.Load() <= probes.Load() {
						t.Error("session probed before local token validation")
					}
					probes.Add(1)
					w.WriteHeader(int(sessionStatus.Load()))
					if sessionStatus.Load() == 200 {
						json.NewEncoder(w).Encode(map[string]any{"cookieName": "penguin_session", "sessionToken": "admin-session", "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339), "maxAgeSeconds": 3600})
					} else {
						fmt.Fprint(w, `{"error":{"code":"not_found"}}`)
					}
				default:
					t.Errorf("unexpected startup path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			cfg := runtimeConfig(t, server.URL)
			rt, err := newClientRuntime(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			mode := rt.HarnessLoginMode()
			rt.Close()
			want := harnessapi.LoginAutomatic
			if initialStatus != 200 {
				want = harnessapi.LoginManual
			}
			if mode != want {
				t.Fatalf("mode=%q want %q", mode, want)
			}
			sessionStatus.Store(404)
			// Exercise the desktop builder as well as the direct CLI constructor.
			app := &appServer{}
			recovered, err := app.buildRuntime(context.Background(), cfg, mode)
			if mode == harnessapi.LoginAutomatic {
				var unsupported *harnessapi.SessionUnsupportedError
				if err == nil || !errors.As(err, &unsupported) {
					if recovered != nil {
						recovered.Close()
					}
					t.Fatalf("automatic recovery degraded: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				recovered.Close()
				if probes.Load() != 1 {
					t.Fatal("manual recovery called session endpoint")
				}
			}
			sessionStatus.Store(200)
			restarted, err := newClientRuntime(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if restarted.HarnessLoginMode() != harnessapi.LoginAutomatic {
				t.Fatal("new start permanently cached old version")
			}
		})
	}
}

func TestRuntimeRejectsAuthorizationFailuresBeforeSessionProbe(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tokenBody   string
		tokenStatus int
		missing     bool
	}{
		{"missing", `{}`, 200, true}, {"invalid token", `{}`, 401, false}, {"forbidden", `{}`, 403, false},
		{"wrong identity", `{"user":{"userId":"other"},"sessionVia":"token"}`, 200, false},
		{"browser session identity", `{"user":{"userId":"admin"},"sessionVia":"password"}`, 200, false},
		{"invalid JSON", `{`, 200, false}, {"HTML", `<html>login</html>`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var probes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/remote/session" {
					probes.Add(1)
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.tokenStatus)
				fmt.Fprint(w, tc.tokenBody)
			}))
			defer server.Close()
			cfg := runtimeConfig(t, server.URL)
			if tc.missing {
				if err := os.Remove(filepath.Join(cfg.Harness.DataRoot, "api-token")); err != nil {
					t.Fatal(err)
				}
			}
			rt, err := newClientRuntime(context.Background(), cfg)
			if rt != nil {
				rt.Close()
				t.Fatal("invalid authorization started runtime")
			}
			var startup *harnessStartupError
			if !errors.As(err, &startup) || startup.code != "harness_authorization_failed" || probes.Load() != 0 {
				t.Fatalf("failure hidden: %v probes=%d", err, probes.Load())
			}
		})
	}
}

func TestRuntimeReportsStoppedHarnessAsUnavailable(t *testing.T) {
	for _, missingToken := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-token=%t", missingToken), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("stopped Harness received a request")
			}))
			cfg := runtimeConfig(t, server.URL)
			server.Close()
			if missingToken {
				if err := os.Remove(filepath.Join(cfg.Harness.DataRoot, "api-token")); err != nil {
					t.Fatal(err)
				}
			}
			rt, err := newClientRuntime(context.Background(), cfg)
			if rt != nil {
				rt.Close()
				t.Fatal("stopped Harness started runtime")
			}
			var startup *harnessStartupError
			if !errors.As(err, &startup) || startup.code != "harness_unavailable" {
				t.Fatalf("stopped Harness reported as authentication failure: %v", err)
			}
		})
	}
}

func TestStatusExposesLoginModeWithoutConnectionError(t *testing.T) {
	for _, mode := range []harnessapi.LoginMode{harnessapi.LoginAutomatic, harnessapi.LoginManual} {
		app, _, server := newTestApp(t, nil, "")
		rt := &fakeRuntime{}
		rt.connected.Store(true)
		app.mu.Lock()
		app.intent = &connectionIntent{loginMode: mode}
		app.tunnel = &runningTunnel{runtime: rt}
		app.runnerStatus = "running"
		app.authStatus = "ready"
		app.mu.Unlock()
		status := fetchStatus(t, server)
		if status["harnessLoginMode"] != string(mode) || status["connectionStatus"] != "connected" || status["lastError"] != "" || status["authStatus"] != "ready" {
			t.Fatalf("incorrect status: %#v", status)
		}
		app.mu.Lock()
		app.intent = nil
		app.tunnel = nil
		app.mu.Unlock()
		if fetchStatus(t, server)["harnessLoginMode"] != "" {
			t.Fatal("mode persisted after stop")
		}
		server.Close()
	}
}
