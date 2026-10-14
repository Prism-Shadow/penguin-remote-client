package harnessapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrepareLoginResponseClassification(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	good := fmt.Sprintf(`{"cookieName":"penguin_session","sessionToken":"remote-session","expiresAt":%q,"maxAgeSeconds":3600}`, expires)
	for _, tc := range []struct {
		name              string
		status            int
		contentType, body string
		wantMode          LoginMode
	}{
		{"success", 200, "application/json", good, LoginAutomatic},
		{"old 404", 404, "application/json", `{"error":{"code":"not_found"}}`, LoginManual},
		{"old 405", 405, "application/json; charset=utf-8", `{"error":"method_not_allowed"}`, LoginManual},
		{"unauthorized", 401, "application/json", `{}`, ""},
		{"forbidden", 403, "application/json", `{}`, ""},
		{"server error", 500, "application/json", `{}`, ""},
		{"unavailable", 503, "application/json", `{}`, ""},
		{"invalid JSON", 200, "application/json", `{`, ""},
		{"invalid session", 200, "application/json", `{}`, ""},
		{"invalid cookie", 200, "application/json", strings.Replace(good, "remote-session", "bad;cookie", 1), ""},
		{"expired session", 200, "application/json", strings.Replace(good, expires, "2000-01-01T00:00:00Z", 1), ""},
		{"wrong cookie name", 200, "application/json", strings.Replace(good, "penguin_session", "other", 1), ""},
		{"invalid max age", 200, "application/json", strings.Replace(good, "3600", "0", 1), ""},
		{"HTML success", 200, "text/html", "<html>login</html>", ""},
		{"HTML 404", 404, "text/html", "<html>not found</html>", ""},
		{"HTML 405", 405, "text/html", "<html>login</html>", ""},
		{"invalid JSON 404", 404, "application/json", `{`, ""},
		{"invalid JSON 405", 405, "application/json", `{`, ""},
		{"mislabeled HTML", 404, "application/json", "<html>login</html>", ""},
		{"empty 404", 404, "application/json", "", ""},
		{"null 404", 404, "application/json", "null", ""},
		{"oversized 404", 404, "application/json", `{"padding":"` + strings.Repeat("a", 64<<10) + `"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/remote/session" || r.Header.Get("Authorization") != "Bearer local-token" || r.Header.Get("Cookie") != "" {
					t.Errorf("unexpected session probe: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, "local-token", server.Client())
			mode, _, err := client.PrepareLogin(context.Background(), "")
			if mode != tc.wantMode || (err != nil) != (tc.wantMode == "") {
				t.Fatalf("mode=%q error=%v; want %q", mode, err, tc.wantMode)
			}
			if mode == LoginManual {
				_, err = client.IssueRemoteSession(context.Background())
				var unsupported *SessionUnsupportedError
				if !errors.As(err, &unsupported) || unsupported.StatusCode != tc.status {
					t.Fatalf("unstructured unsupported error: %v", err)
				}
			}
		})
	}
}

func TestPrepareLoginModeLifetime(t *testing.T) {
	var calls atomic.Int32
	var status atomic.Int32
	status.Store(404)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(status.Load()))
		if status.Load() == 200 {
			json.NewEncoder(w).Encode(map[string]any{"cookieName": SessionCookieName, "sessionToken": "new-session", "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339), "maxAgeSeconds": 3600})
		} else {
			fmt.Fprint(w, `{"error":"unsupported"}`)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "token", server.Client())
	mode, _, err := client.PrepareLogin(context.Background(), "")
	if err != nil || mode != LoginManual {
		t.Fatalf("first probe: %q %v", mode, err)
	}
	mode, _, err = client.PrepareLogin(context.Background(), LoginManual)
	if err != nil || mode != LoginManual || calls.Load() != 1 {
		t.Fatalf("manual recovery probed session: %d %v", calls.Load(), err)
	}
	if _, _, err = client.PrepareLogin(context.Background(), LoginAutomatic); err == nil {
		t.Fatal("automatic recovery silently fell back")
	}
	status.Store(200)
	mode, _, err = client.PrepareLogin(context.Background(), "")
	if err != nil || mode != LoginAutomatic || calls.Load() != 3 {
		t.Fatalf("new intent did not redetect: %q %d %v", mode, calls.Load(), err)
	}
}

func TestClientRejectsRedirectsWithoutCookies(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Cookie"), "desktop-admin") {
			t.Error("local browser cookie leaked into API probe")
		}
		if r.URL.Path == "/destination" {
			redirected.Add(1)
			w.WriteHeader(404)
			return
		}
		http.Redirect(w, r, "/destination", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	httpClient := server.Client()
	httpClient.Jar, _ = cookiejar.New(nil)
	u, _ := url.Parse(server.URL)
	httpClient.Jar.SetCookies(u, []*http.Cookie{{Name: SessionCookieName, Value: "desktop-admin"}})
	client, _ := NewClient(server.URL, "token", httpClient)
	if err := client.ValidateAPIToken(context.Background()); err == nil {
		t.Fatal("token redirect accepted")
	}
	if _, _, err := client.PrepareLogin(context.Background(), ""); err == nil {
		t.Fatal("session redirect accepted")
	}
	if _, err := client.ValidateSession(context.Background(), "session"); err == nil {
		t.Fatal("validation redirect accepted")
	}
	if redirected.Load() != 0 {
		t.Fatal("API redirects followed")
	}
	if httpClient.Jar == nil {
		t.Fatal("caller client mutated")
	}
}

func TestPrepareLoginTransportFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	client, _ := NewClient(server.URL, "token", &http.Client{Timeout: 20 * time.Millisecond})
	if _, _, err := client.PrepareLogin(context.Background(), ""); err == nil {
		t.Fatal("timeout accepted")
	}
	server.Close()
	if _, _, err := client.PrepareLogin(context.Background(), ""); err == nil {
		t.Fatal("connection failure accepted")
	}
}

func TestValidateSessionRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{"<html>login</html>", `{`, `{}`, `{"user":null}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		}))
		client, _ := NewClient(server.URL, "token", server.Client())
		if valid, err := client.ValidateSession(context.Background(), "session"); valid || err == nil {
			t.Errorf("invalid session response accepted: %q", body)
		}
		server.Close()
	}
}
