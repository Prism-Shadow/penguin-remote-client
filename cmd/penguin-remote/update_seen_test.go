package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateSeenPersistsAcrossRestart(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"state":"available","version":"0.1.6"}`)
	}))
	defer bridge.Close()
	config := filepath.Join(t.TempDir(), "config.json")
	a := &appServer{baseURL: "http://127.0.0.1:18973", configPath: config, desktopUpdate: newDesktopUpdateBridge(bridge.URL, "secret")}
	if loadUpdateSeen(config) != "" {
		t.Fatal("fresh install must not have a viewed version")
	}
	req := httptest.NewRequest("POST", a.baseURL+"/api/preferences/update-seen", strings.NewReader(`{"version":"0.1.6"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", a.baseURL)
	req.Header.Set("X-Penguin-Update", "1")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != 200 || loadUpdateSeen(config) != "0.1.6" {
		t.Fatalf("receipt not saved: %d %s", w.Code, w.Body.String())
	}
	restarted := &appServer{baseURL: a.baseURL, configPath: config, desktopUpdate: a.desktopUpdate, updateSeenVersion: loadUpdateSeen(config)}
	w = httptest.NewRecorder()
	restarted.routes().ServeHTTP(w, httptest.NewRequest("GET", a.baseURL+"/api/desktop/update", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"seenVersion":"0.1.6"`) {
		t.Fatalf("receipt not restored: %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateSeenRejectsInvalidRequests(t *testing.T) {
	a := &appServer{baseURL: "http://127.0.0.1:18973", configPath: filepath.Join(t.TempDir(), "config.json")}
	for _, tc := range []struct {
		method, body, origin, contentType, header string
		code                                      int
	}{
		{"GET", "", a.baseURL, "application/json", "1", 405},
		{"POST", `{"version":"0.1.6"}`, "https://evil.example", "application/json", "1", 403},
		{"POST", `{"version":"0.1.6"}`, "", "application/json", "1", 403},
		{"POST", `{"version":"0.1.6"}`, a.baseURL, "application/json", "", 403},
		{"POST", `{"version":"0.1.6"}`, a.baseURL, "text/plain", "1", 415},
		{"POST", `{"version":"../config"}`, a.baseURL, "application/json", "1", 400},
		{"POST", `{"version":"0.1.6-beta.1"}`, a.baseURL, "application/json", "1", 400},
		{"POST", `{`, a.baseURL, "application/json", "1", 400},
	} {
		r := httptest.NewRequest(tc.method, a.baseURL+"/api/preferences/update-seen", strings.NewReader(tc.body))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.contentType)
		r.Header.Set("X-Penguin-Update", tc.header)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.body, w.Code, tc.code)
		}
	}
	if loadUpdateSeen(a.configPath) != "" {
		t.Fatal("invalid requests saved a receipt")
	}
}
