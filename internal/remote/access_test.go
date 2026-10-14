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
