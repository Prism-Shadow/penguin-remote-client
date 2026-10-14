package remoteapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeviceEndpointsBearerAndSecretRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer device-secret" || r.Header.Get("Cookie") != "" {
			t.Error("device endpoint credential scope wrong")
		}
		w.WriteHeader(409)
		json.NewEncoder(w).Encode(map[string]any{"reason": "grant_superseded", "error": "device-secret tunnel-secret"})
	}))
	defer server.Close()
	c, _ := NewClient(ClientConfig{BaseURL: server.URL})
	_, err := c.Acquire(context.Background(), "device-secret", AcquireRequest{RequestID: "click", HarnessLocalURL: "http://127.0.0.1:7364"})
	if ErrorCode(err) != "grant_superseded" || Retryable(err) || strings.Contains(err.Error(), "device-secret") || strings.Contains(err.Error(), "tunnel-secret") {
		t.Fatal("unsafe acquire error")
	}
	_, err = c.Status(context.Background(), "device-secret")
	if ErrorCode(err) != "grant_superseded" {
		t.Fatal("status contract wrong")
	}
}

func TestCredentialAPIRequiresTLSAndNeverForwardsRedirects(t *testing.T) {
	for _, raw := range []string{"http://go.example.com", "https://user:private-secret@go.example.com", "https://go.example.com?token=private-secret"} {
		if _, err := NewClient(ClientConfig{BaseURL: raw}); err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("unsafe API URL accepted or echoed")
		}
	}
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(307)
	}))
	defer api.Close()
	cli, _ := NewClient(ClientConfig{BaseURL: api.URL, HTTPClient: &http.Client{}})
	if _, err := cli.Status(context.Background(), "private-secret"); err == nil || forwarded {
		t.Fatal("device credential followed an API redirect")
	}
}

func TestCancelExactContractAndAcquireCancelledReason(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer device-secret" {
			t.Error("cancel did not use device credential")
		}
		if r.URL.Path == "/api/remote/client/tunnel/cancel" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 2 || body["protocolVersion"] != float64(2) || body["requestId"] != "original" {
				t.Error("cancel request fields differ from contract")
			}
			w.Write([]byte(`{"protocolVersion":2,"cancelled":true}`))
			return
		}
		w.WriteHeader(409)
		w.Write([]byte(`{"reason":"acquire_cancelled","retryable":false}`))
	}))
	defer api.Close()
	client, _ := NewClient(ClientConfig{BaseURL: api.URL})
	if err := client.Cancel(context.Background(), "device-secret", CancelRequest{RequestID: "original"}); err != nil {
		t.Fatal(err)
	}
	_, err := client.Acquire(context.Background(), "device-secret", AcquireRequest{RequestID: "original"})
	if ErrorCode(err) != "acquire_cancelled" || Retryable(err) {
		t.Fatal("cancelled request was not a terminal refusal")
	}
}

func TestCancelInvalidSuccessNeverConfirmsOrRetriesAsNetworkFailure(t *testing.T) {
	for _, body := range []string{`{"protocolVersion":2,"cancelled":false}`, `{"cancelled":true}`, `<html>private-secret</html>`} {
		t.Run(body, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer api.Close()
			client, _ := NewClient(ClientConfig{BaseURL: api.URL})
			err := client.Cancel(context.Background(), "device-secret", CancelRequest{RequestID: "original"})
			if err == nil || Retryable(err) || strings.Contains(err.Error(), "private-secret") {
				t.Fatal("invalid success was confirmed, retried as network failure, or exposed")
			}
		})
	}
}
