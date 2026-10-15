package harnessapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientValidatesTokenAndIssuesRemoteSession(t *testing.T) {
	const apiToken = "local-api-token"
	const sessionToken = "remote-session-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/me":
			if r.Header.Get("Authorization") == "Bearer "+apiToken {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"user":       map[string]string{"userId": "admin"},
					"sessionVia": "token",
				})
				return
			}
			if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value == sessionToken {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"user":       map[string]string{"userId": "admin"},
					"sessionVia": "password",
				})
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/api/remote/session":
			if r.Header.Get("Authorization") != "Bearer "+apiToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cookieName":    SessionCookieName,
				"sessionToken":  sessionToken,
				"expiresAt":     time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
				"maxAgeSeconds": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, apiToken, server.Client())
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if err := client.ValidateAPIToken(context.Background()); err != nil {
		t.Fatalf("ValidateAPIToken returned error: %v", err)
	}
	session, err := client.IssueRemoteSession(context.Background())
	if err != nil {
		t.Fatalf("IssueRemoteSession returned error: %v", err)
	}
	if session.SessionToken != sessionToken || session.CookieName != SessionCookieName {
		t.Fatalf("unexpected session: %#v", session)
	}
	valid, err := client.ValidateSession(context.Background(), sessionToken)
	if err != nil {
		t.Fatalf("ValidateSession returned error: %v", err)
	}
	if !valid {
		t.Fatal("issued session was not valid")
	}
}

func TestClientRejectsNonLoopbackHarness(t *testing.T) {
	if _, err := NewClient("http://192.0.2.10:7364", "token", nil); err == nil {
		t.Fatal("NewClient accepted a non-loopback Harness URL")
	}
}
