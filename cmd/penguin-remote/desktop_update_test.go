package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDesktopUpdateRelay(t *testing.T) {
	var commands atomic.Int32
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-shell-secret" {
			t.Error("missing private bridge credential")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("browser headers forwarded to private bridge")
		}
		if r.Method == "POST" {
			commands.Add(1)
			w.WriteHeader(202)
		} else {
			fmt.Fprint(w, `{"appVersion":"0.1.5","state":"available","version":"0.1.6"}`)
		}
	}))
	defer bridge.Close()
	a := &appServer{baseURL: "http://127.0.0.1:18973", desktopUpdate: newDesktopUpdateBridge(bridge.URL, "private-shell-secret")}
	for _, tc := range []struct {
		method, suffix, origin, header, host string
		status                               int
	}{
		{"GET", "", "", "", "127.0.0.1:18973", 200},
		{"POST", "/check", a.baseURL, "1", "127.0.0.1:18973", 202},
		{"POST", "/download", a.baseURL, "1", "127.0.0.1:18973", 202},
		{"POST", "/install", a.baseURL, "1", "127.0.0.1:18973", 202},
		{"POST", "/install", "https://evil.example", "1", "127.0.0.1:18973", 403},
		{"POST", "/install", "", "1", "127.0.0.1:18973", 403},
		{"POST", "/install", a.baseURL, "", "127.0.0.1:18973", 403},
		{"GET", "", "", "", "127.0.0.1:9999", 403},
		{"GET", "/install", "", "", "127.0.0.1:18973", 405},
		{"POST", "/execute", a.baseURL, "1", "127.0.0.1:18973", 404},
	} {
		t.Run(tc.method+tc.suffix+tc.origin+tc.host+tc.header, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, a.baseURL+"/api/desktop/update"+tc.suffix, nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Penguin-Update", tc.header)
			r.Header.Set("Cookie", "private-browser-cookie")
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-shell-secret") {
				t.Fatal("credential leaked")
			}
		})
	}
	if commands.Load() != 3 {
		t.Fatalf("forwarded %d commands", commands.Load())
	}
}

func TestDesktopUpdateStandaloneAndUnavailable(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:1", "http://example.com:1", "http://127.0.0.1:1/path", "http://user:pass@127.0.0.1:1", "http://127.0.0.1:1?redirect=x"} {
		if newDesktopUpdateBridge(raw, "secret") != nil {
			t.Fatalf("accepted invalid bridge %q", raw)
		}
	}
	if newDesktopUpdateBridge("http://127.0.0.1:1", "") != nil {
		t.Fatal("accepted empty credential")
	}
	a := &appServer{baseURL: "http://127.0.0.1:18973"}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", a.baseURL+"/api/desktop/update", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "no-shell") {
		t.Fatal(w.Body.String())
	}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
	a.desktopUpdate = newDesktopUpdateBridge(bridge.URL, "secret")
	bridge.Close()
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", a.baseURL+"/api/desktop/update", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Body.String())
	}
}
