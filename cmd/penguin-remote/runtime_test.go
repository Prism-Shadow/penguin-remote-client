package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
