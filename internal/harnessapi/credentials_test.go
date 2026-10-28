package harnessapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeRotatedSession(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"cookieName": SessionCookieName, "sessionToken": "rotated-session", "expiresAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "maxAgeSeconds": 3600})
}

func TestConcurrentSessionRequestsShareValidatedTokenRefresh(t *testing.T) {
	const callers = 16
	var rejected, validated, issued, reads atomic.Int32
	allRejected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("browser cookies reached token authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/remote/session" && r.Header.Get("Authorization") == "Bearer old-token" {
			if rejected.Add(1) == callers {
				close(allRejected)
			}
			select {
			case <-allRejected:
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(401)
			fmt.Fprint(w, `{}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer new-token" {
			t.Error("unexpected credential")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/me":
			validated.Add(1)
			fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"token"}`)
		case "/api/remote/session":
			if validated.Load() == 0 {
				t.Error("replacement token used before validation")
			}
			issued.Add(1)
			writeRotatedSession(w)
		default:
			t.Error("unexpected endpoint")
		}
	}))
	defer server.Close()
	client, err := NewClientWithTokenSource(server.URL, "old-token", server.Client(), func() (string, error) {
		reads.Add(1)
		return " new-token\n", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.IssueRemoteSession(ctx); err != nil {
				t.Errorf("session did not recover: %v", err)
			}
		}()
	}
	wg.Wait()
	if reads.Load() != 1 || validated.Load() != 1 || issued.Load() != callers {
		t.Fatalf("reads=%d validations=%d sessions=%d", reads.Load(), validated.Load(), issued.Load())
	}
	if _, err := client.IssueRemoteSession(ctx); err != nil || reads.Load() != 1 || rejected.Load() != callers {
		t.Fatalf("replacement token was not retained: %v", err)
	}
}

func TestTokenRefreshFailsClosedAndBoundsRetries(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sessionStatus    int
		token            string
		readError        bool
		validationStatus int
		validationBody   string
		validationType   string
		wantReads        int32
		wantValidations  int32
		wantSessionCalls int32
	}{
		{name: "403 is not rotation", sessionStatus: 403, wantSessionCalls: 1},
		{name: "500 is not rotation", sessionStatus: 500, wantSessionCalls: 1},
		{name: "404 keeps compatibility", sessionStatus: 404, wantSessionCalls: 1},
		{name: "405 keeps compatibility", sessionStatus: 405, wantSessionCalls: 1},
		{name: "redirect is not followed", sessionStatus: 307, wantSessionCalls: 1},
		{name: "token unchanged", sessionStatus: 401, token: "old-token", wantReads: 1, wantSessionCalls: 1},
		{name: "token empty", sessionStatus: 401, wantReads: 1, wantSessionCalls: 1},
		{name: "token file unavailable", sessionStatus: 401, readError: true, wantReads: 1, wantSessionCalls: 1},
		{name: "replacement refused", sessionStatus: 401, token: "new-token", validationStatus: 401, wantReads: 1, wantValidations: 1, wantSessionCalls: 1},
		{name: "wrong user", sessionStatus: 401, token: "new-token", validationStatus: 200, validationBody: `{"user":{"userId":"other"},"sessionVia":"token"}`, wantReads: 1, wantValidations: 1, wantSessionCalls: 1},
		{name: "cookie authority rejected", sessionStatus: 401, token: "new-token", validationStatus: 200, validationBody: `{"user":{"userId":"admin"},"sessionVia":"remote"}`, wantReads: 1, wantValidations: 1, wantSessionCalls: 1},
		{name: "HTML validation", sessionStatus: 401, token: "new-token", validationStatus: 200, validationType: "text/html", validationBody: `<html>login</html>`, wantReads: 1, wantValidations: 1, wantSessionCalls: 1},
		{name: "invalid JSON validation", sessionStatus: 401, token: "new-token", validationStatus: 200, validationBody: `{`, wantReads: 1, wantValidations: 1, wantSessionCalls: 1},
		{name: "validation redirect refused", sessionStatus: 401, token: "new-token", validationStatus: 307, wantReads: 1, wantValidations: 1, wantSessionCalls: 1},
		{name: "second 401 never loops", sessionStatus: 401, token: "new-token", validationStatus: 200, validationBody: `{"user":{"userId":"admin"},"sessionVia":"token"}`, wantReads: 1, wantValidations: 1, wantSessionCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, validations, calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/me":
					validations.Add(1)
					if tc.validationType != "" {
						w.Header().Set("Content-Type", tc.validationType)
					}
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(tc.validationStatus)
					fmt.Fprint(w, tc.validationBody)
				case "/api/remote/session":
					calls.Add(1)
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(tc.sessionStatus)
					fmt.Fprint(w, `{}`)
				default:
					t.Error("redirect followed")
				}
			}))
			defer server.Close()
			client, err := NewClientWithTokenSource(server.URL, "old-token", server.Client(), func() (string, error) {
				reads.Add(1)
				if tc.readError {
					return "", errors.New("unavailable")
				}
				return tc.token, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.IssueRemoteSession(context.Background()); err == nil {
				t.Fatal("invalid recovery succeeded")
			}
			if reads.Load() != tc.wantReads || validations.Load() != tc.wantValidations || calls.Load() != tc.wantSessionCalls {
				t.Fatalf("reads=%d validations=%d calls=%d", reads.Load(), validations.Load(), calls.Load())
			}
			if tc.wantValidations > 0 && tc.wantSessionCalls == 1 && client.currentAPIToken() != "old-token" {
				t.Fatal("unvalidated replacement was cached")
			}
		})
	}
}

func TestFailedTokenRefreshIsThrottledAndLogsNoSecrets(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "secret-response-body")
	}))
	defer server.Close()
	var reads int
	client, _ := NewClientWithTokenSource(server.URL, "secret-old-token", server.Client(), func() (string, error) {
		reads++
		return "", errors.New("secret-file-path and secret-file-token")
	})
	for range 5 {
		if _, err := client.IssueRemoteSession(context.Background()); err == nil || strings.Contains(err.Error(), "secret-") {
			t.Fatalf("unsafe or missing recovery error: %v", err)
		}
	}
	if reads != 1 || strings.Contains(output.String(), "secret-") || strings.Contains(output.String(), server.URL) || !strings.Contains(output.String(), "reason=token_read_failed") {
		t.Fatalf("refresh was not throttled or logs unsafe: reads=%d log=%s", reads, output.String())
	}
	// A later browser retry must be allowed to attempt recovery again.
	time.Sleep(tokenRefreshBackoff + 10*time.Millisecond)
	client.IssueRemoteSession(context.Background())
	if reads != 2 {
		t.Fatal("failed refresh permanently disabled recovery")
	}
}

func TestTokenRefreshRespectsCancellation(t *testing.T) {
	validationStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			close(validationStarted)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(401)
	}))
	defer server.Close()
	client, _ := NewClientWithTokenSource(server.URL, "old-token", server.Client(), func() (string, error) { return "new-token", nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.IssueRemoteSession(ctx)
		done <- err
	}()
	select {
	case <-validationStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement token validation never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || client.currentAPIToken() != "old-token" {
			t.Fatal("canceled validation retained a replacement credential")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop token validation")
	}
}

func TestSlowValidationFailureStartsBackoffAfterCompletion(t *testing.T) {
	var validations, reads atomic.Int32
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" {
			validations.Add(1)
			if !available.Load() {
				// The failed validation itself outlasts the entire backoff interval.
				time.Sleep(tokenRefreshBackoff + 50*time.Millisecond)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"user":{"userId":"admin"},"sessionVia":"token"}`)
			return
		}
		if available.Load() && r.Header.Get("Authorization") == "Bearer new-token" {
			writeRotatedSession(w)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := NewClientWithTokenSource(server.URL, "old-token", server.Client(), func() (string, error) {
		reads.Add(1)
		return "new-token", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.IssueRemoteSession(context.Background()); err == nil {
			t.Fatal("refused replacement token unexpectedly succeeded")
		}
	}
	if reads.Load() != 1 || validations.Load() != 1 {
		t.Fatalf("slow failure consumed the backoff: reads=%d validations=%d", reads.Load(), validations.Load())
	}
	available.Store(true)
	time.Sleep(tokenRefreshBackoff + 10*time.Millisecond)
	if _, err := client.IssueRemoteSession(context.Background()); err != nil {
		t.Fatalf("recovery after the backoff failed: %v", err)
	}
	if reads.Load() != 2 || validations.Load() != 2 {
		t.Fatalf("unexpected recovery attempts: reads=%d validations=%d", reads.Load(), validations.Load())
	}
}
