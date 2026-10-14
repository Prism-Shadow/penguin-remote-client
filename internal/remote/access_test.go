package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHarnessAccessMatchesConfiguredDataRoot(t *testing.T) {
	const token = "matching-local-api-token"
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "api-token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user":       map[string]string{"userId": "admin"},
			"sessionVia": "token",
		})
	}))
	defer server.Close()

	access, err := ResolveHarnessAccess(context.Background(), server.URL, root)
	if err != nil {
		t.Fatalf("ResolveHarnessAccess returned error: %v", err)
	}
	if access.DataRoot != root || access.APIToken != token {
		t.Fatalf("unexpected access: %#v", access)
	}
}

func TestExplicitHarnessDataRootDoesNotFallBack(t *testing.T) {
	configured := t.TempDir()
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "api-token"), []byte("matching-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PENGUIN_HOME", other)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer matching-token" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"user":{"userId":"admin"},"sessionVia":"token"}`))
	}))
	defer server.Close()
	if _, err := ResolveHarnessAccess(context.Background(), server.URL, configured); err == nil {
		t.Fatal("missing explicit data directory token hidden by another installation")
	}
	if err := os.WriteFile(filepath.Join(configured, "api-token"), []byte("invalid-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveHarnessAccess(context.Background(), server.URL, configured); err == nil {
		t.Fatal("invalid explicit data directory token hidden by another installation")
	}
}
