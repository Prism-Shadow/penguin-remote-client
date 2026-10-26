package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveViteBackendRequiresMatchingToken(t *testing.T) {
	for _, matches := range []bool{false, true} {
		t.Run(fmt.Sprint(matches), func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !matches || r.Header.Get("Authorization") != "Bearer selected-token" {
					w.WriteHeader(401)
					return
				}
				fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"token"}`)
			}))
			defer backend.Close()
			web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `<title>PenguinHarness</title><script src="/@vite/client"></script>`)
			}))
			defer web.Close()
			root := t.TempDir()
			u, _ := url.Parse(backend.URL)
			os.WriteFile(filepath.Join(root, "server.lock"), []byte(fmt.Sprintf(`{"port":%s}`, u.Port())), 0600)
			got, err := ResolveHarnessAPIURL(context.Background(), web.URL, HarnessAccess{DataRoot: root, APIToken: "selected-token"})
			if matches {
				if err != nil || got != "http://localhost:"+u.Port() {
					t.Fatalf("backend=%q err=%v", got, err)
				}
			} else if err == nil {
				t.Fatal("foreign backend was accepted")
			}
		})
	}
}

func TestResolveViteBackendRejectsBadLocks(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<script src="/@vite/client"></script>`) }))
	defer web.Close()
	u, _ := url.Parse(web.URL)
	for _, lock := range []string{"", "{}", "{", `{"port":0}`, `{"port":65536}`, fmt.Sprintf(`{"port":%s}`, u.Port())} {
		t.Run(lock, func(t *testing.T) {
			root := t.TempDir()
			if lock != "" {
				os.WriteFile(filepath.Join(root, "server.lock"), []byte(lock), 0600)
			}
			if _, err := ResolveHarnessAPIURL(context.Background(), web.URL, HarnessAccess{DataRoot: root, APIToken: "token"}); err == nil {
				t.Fatal("invalid lock accepted")
			}
		})
	}
}

func TestResolvePackagedHarnessKeepsSelectedAddress(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<title>PenguinHarness</title>`) }))
	defer web.Close()
	got, err := ResolveHarnessAPIURL(context.Background(), web.URL, HarnessAccess{})
	if err != nil || !strings.EqualFold(got, web.URL) {
		t.Fatalf("target changed: %q %v", got, err)
	}
}
