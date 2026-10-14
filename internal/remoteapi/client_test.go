package remoteapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientStartsDeviceAndGrantsTunnel(t *testing.T) {
	var sawOrigin string
	var sawCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawOrigin = r.Header.Get("Origin")
		sawCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/remote/auth/start":
			var req DeviceStartRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode start request: %v", err)
			}
			if req.DeviceName != "office-pc" || req.HarnessLocalURL != "http://127.0.0.1:7364" {
				t.Fatalf("unexpected start request: %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"deviceId":"remote_device_1","userId":42,"status":"authorized"}`))
		case "/api/remote/tunnel/grant":
			var req TunnelGrantRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode grant request: %v", err)
			}
			if req.DeviceID != "remote_device_1" {
				t.Fatalf("unexpected grant request: %+v", req)
			}
			_, _ = w.Write([]byte(`{"deviceId":"remote_device_1","serverAddr":"frps.example.com","serverPort":7000,"token":"tunnel-token","customDomain":"abc.remote.example.com","publicUrl":"https://abc.remote.example.com","proxyName":"penguin-harness"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:       server.URL,
		SessionCookie: "relay_session=session-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	device, err := client.StartDevice(context.Background(), DeviceStartRequest{
		DeviceName:      "office-pc",
		Platform:        "windows",
		HarnessLocalURL: "http://127.0.0.1:7364",
	})
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceID != "remote_device_1" || string(device.UserID) != "42" {
		t.Fatalf("unexpected device: %+v", device)
	}
	grant, err := client.GrantTunnel(context.Background(), TunnelGrantRequest{
		DeviceID:        device.DeviceID,
		HarnessLocalURL: "http://127.0.0.1:7364",
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.ServerAddr != "frps.example.com" || grant.CustomDomain != "abc.remote.example.com" {
		t.Fatalf("unexpected grant: %+v", grant)
	}
	if sawOrigin != server.URL {
		t.Fatalf("Origin = %q, want %q", sawOrigin, server.URL)
	}
	if sawCookie != "relay_session=session-token" {
		t.Fatalf("Cookie = %q", sawCookie)
	}
}

func TestClientAcceptsRawRelaySessionValue(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:       "https://go.example.com",
		SessionCookie: "raw-session-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.sessionCookie != "relay_session=raw-session-value" {
		t.Fatalf("sessionCookie = %q", client.sessionCookie)
	}
}

func TestClientBuildsClientLoginURL(t *testing.T) {
	client, err := NewClient(ClientConfig{BaseURL: "https://go.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	loginURL := client.ClientLoginURL("http://127.0.0.1:12345/callback", DeviceStartRequest{
		DeviceName:      "office-pc",
		Platform:        "windows",
		HarnessLocalURL: "http://127.0.0.1:7364",
	}, "state-123", "instance-abc")
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/api/remote/client/login" {
		t.Fatalf("path = %q", parsed.Path)
	}
	q := parsed.Query()
	if q.Get("return_to") != "http://127.0.0.1:12345/callback" {
		t.Fatalf("return_to = %q", q.Get("return_to"))
	}
	if q.Get("state") != "state-123" {
		t.Fatalf("state = %q", q.Get("state"))
	}
	if q.Get("device_name") != "office-pc" || q.Get("platform") != "windows" {
		t.Fatalf("unexpected device query: %s", parsed.RawQuery)
	}
	// The installation id travels with the login link, so a link opened later
	// still belongs to the installation that created it.
	if q.Get("client_instance_id") != "instance-abc" {
		t.Fatalf("client_instance_id = %q", q.Get("client_instance_id"))
	}
}

func TestClientLoginURLOmitsAnEmptyClientInstanceID(t *testing.T) {
	client, err := NewClient(ClientConfig{BaseURL: "https://go.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	loginURL := client.ClientLoginURL("http://127.0.0.1:12345/callback", DeviceStartRequest{
		DeviceName: "office-pc",
		Platform:   "windows",
	}, "state-123", "")
	parsed, err := url.Parse(loginURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Has("client_instance_id") {
		t.Fatalf("empty client instance id was sent: %s", parsed.RawQuery)
	}
}

func TestClientExchangesClientCodeWithoutCookie(t *testing.T) {
	var sawCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCookie = r.Header.Get("Cookie")
		if r.URL.Path != "/api/remote/client/exchange-code" {
			http.NotFound(w, r)
			return
		}
		var req ClientLoginExchangeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode exchange request: %v", err)
		}
		if req.Code != "remote_client_code_1" || req.State != "state-123" {
			t.Fatalf("unexpected exchange request: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device":{"deviceId":"remote_device_1","userId":42,"status":"authorized"},"entitlement":{"status":"active","expiresAt":"2026-10-22T08:00:00.000Z"},"tunnel":{"deviceId":"remote_device_1","serverAddr":"frps.example.com","serverPort":7000,"token":"tunnel-token","customDomain":"abc.remote.example.com","publicUrl":"https://abc.remote.example.com","proxyName":"penguin-harness"},"user":{"id":"42","username":"penguin-user","displayName":"Penguin User"}}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ExchangeClientCode(context.Background(), ClientLoginExchangeRequest{
		Code:            "remote_client_code_1",
		State:           "state-123",
		HarnessLocalURL: "http://127.0.0.1:7364",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Device.DeviceID != "remote_device_1" {
		t.Fatalf("unexpected exchange result: %+v", result)
	}
	if result.Tunnel == nil || result.Tunnel.ServerAddr != "frps.example.com" {
		t.Fatalf("active subscription did not decode its tunnel: %+v", result.Tunnel)
	}
	if result.Entitlement.Status != EntitlementStatusActive {
		t.Fatalf("status = %q, want active", result.Entitlement.Status)
	}
	if result.User == nil || result.User.Username != "penguin-user" || result.User.DisplayName != "Penguin User" {
		t.Fatalf("unexpected exchange user: %+v", result.User)
	}
	if sawCookie != "" {
		t.Fatalf("Cookie = %q, want empty", sawCookie)
	}
}

// Penguin Go uses reason for the machine-readable takeover code and error for
// the human message. The redacted lease endpoint must retain the former so the
// client can stop immediately and show the superseded state.
func TestRenewLeaseReadsSupersededReasonFromPenguinGo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != LeasePath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"Remote client lease is no longer current","reason":"remote_lease_superseded"}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.RenewLease(context.Background(), LeaseRenewRequest{
		DeviceID: "remote_device_1",
		LeaseID:  "lease-1",
	})
	if err == nil {
		t.Fatal("RenewLease accepted a superseded lease")
	}
	if code := ErrorCode(err); code != LeaseCodeSuperseded {
		t.Fatalf("ErrorCode = %q, want %q (error: %v)", code, LeaseCodeSuperseded, err)
	}
	if strings.Contains(err.Error(), "no longer current") {
		t.Fatalf("redacted lease error exposed the human response body: %v", err)
	}
}

func TestClientExchangesActiveEntitlement(t *testing.T) {
	result, err := exchangeClientCode(t, `{
		"device": {"deviceId": "remote_device_1", "userId": 42},
		"entitlement": {"status": "active", "expiresAt": "2026-10-22T08:00:00.000Z"},
		"tunnel": {"deviceId": "remote_device_1", "serverAddr": "frps.example.com", "serverPort": 7000, "token": "tunnel-token", "customDomain": "abc.remote.example.com", "publicUrl": "https://abc.remote.example.com"}
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if result.Entitlement.Status != EntitlementStatusActive {
		t.Fatalf("status = %q, want active", result.Entitlement.Status)
	}
	expiresAt, ok, err := result.Entitlement.ExpiresAtTime()
	if err != nil || !ok {
		t.Fatalf("ExpiresAtTime() = %v, %v, %v", expiresAt, ok, err)
	}
	if want := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC); !expiresAt.Equal(want) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, want)
	}
	if result.Tunnel == nil {
		t.Fatal("tunnel = nil, want the grant an active subscription must carry")
	}
	if result.Tunnel.Token != "tunnel-token" {
		t.Fatalf("tunnel token = %q", result.Tunnel.Token)
	}
}

func TestClientExchangesExpiredEntitlementWithNullTunnel(t *testing.T) {
	result, err := exchangeClientCode(t, `{
		"device": {"deviceId": "remote_device_1", "userId": 42},
		"entitlement": {"status": "expired", "expiresAt": "2026-08-22T08:00:00.000Z"},
		"tunnel": null
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if result.Entitlement.Status != EntitlementStatusExpired {
		t.Fatalf("status = %q, want expired", result.Entitlement.Status)
	}
	expiresAt, ok, err := result.Entitlement.ExpiresAtTime()
	if err != nil || !ok {
		t.Fatalf("ExpiresAtTime() = %v, %v, %v", expiresAt, ok, err)
	}
	if !expiresAt.Before(time.Now().UTC()) {
		t.Fatalf("expired expiresAt = %s, want a past time", expiresAt)
	}
	if result.Tunnel != nil {
		t.Fatalf("tunnel = %+v, want nil for an expired subscription", result.Tunnel)
	}
}

func TestClientExchangesNoneEntitlementWithNullTunnel(t *testing.T) {
	result, err := exchangeClientCode(t, `{
		"device": {"deviceId": "remote_device_1", "userId": 42},
		"entitlement": {"status": "none", "expiresAt": null},
		"tunnel": null
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if result.Entitlement.Status != EntitlementStatusNone {
		t.Fatalf("status = %q, want none", result.Entitlement.Status)
	}
	if _, ok, err := result.Entitlement.ExpiresAtTime(); ok || err != nil {
		t.Fatalf("ExpiresAtTime() reported presence for a null expiry: ok=%v err=%v", ok, err)
	}
	if result.Tunnel != nil {
		t.Fatalf("tunnel = %+v, want nil", result.Tunnel)
	}
}

// `"tunnel": null` must never decode into a zero-valued grant that looks usable.
func TestClientNeverDecodesNullTunnelAsZeroGrant(t *testing.T) {
	for name, body := range map[string]string{
		"explicit null": `{"device":{"deviceId":"remote_device_1"},"entitlement":{"status":"none"},"tunnel":null}`,
		"absent field":  `{"device":{"deviceId":"remote_device_1"},"entitlement":{"status":"none"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			result, err := exchangeClientCode(t, body)
			if err != nil {
				t.Fatal(err)
			}
			if result.Tunnel != nil {
				t.Fatalf("tunnel = %+v, want nil", result.Tunnel)
			}
		})
	}
}

func TestEntitlementExpiresAtTimeReportsUnparsableValues(t *testing.T) {
	raw := "not-a-timestamp"
	entitlement := RemoteEntitlement{Status: EntitlementStatusActive, ExpiresAt: &raw}

	if _, ok, err := entitlement.ExpiresAtTime(); err == nil || ok {
		t.Fatalf("ExpiresAtTime() accepted an unparsable expiry: ok=%v err=%v", ok, err)
	}
}

func TestEntitlementStatusKnown(t *testing.T) {
	for _, status := range []EntitlementStatus{
		EntitlementStatusActive,
		EntitlementStatusExpired,
		EntitlementStatusNone,
	} {
		if !status.Known() {
			t.Fatalf("status %q reported as unknown", status)
		}
	}
	for _, status := range []EntitlementStatus{"", "trial", "ACTIVE"} {
		if status.Known() {
			t.Fatalf("status %q reported as known", status)
		}
	}
}

// Penguin Go model API keys must never be received or carried around by the
// client, so an unexpected api-key field is dropped at the decode boundary.
func TestClientDropsUnmodeledAPIKeysFromTheResponse(t *testing.T) {
	result, err := exchangeClientCode(t, `{
		"device": {"deviceId": "remote_device_1", "apiKey": "penguin-model-key"},
		"entitlement": {"status": "none", "expiresAt": null, "modelApiKey": "penguin-model-key"},
		"tunnel": null,
		"apiKey": "penguin-model-key"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "penguin-model-key") {
		t.Fatalf("the exchange response retained an API key: %s", encoded)
	}
}

func exchangeClientCode(t *testing.T, body string) (*ClientLoginExchangeResponse, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/remote/client/exchange-code" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		return nil, err
	}
	return client.ExchangeClientCode(context.Background(), ClientLoginExchangeRequest{
		Code:            "remote_client_code_1",
		State:           "state-123",
		HarnessLocalURL: "http://127.0.0.1:7364",
	})
}
