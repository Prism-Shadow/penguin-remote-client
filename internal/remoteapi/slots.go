package remoteapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const ProtocolVersion = 2

type SlotGrant struct {
	GrantID         string `json:"grantId"`
	SlotIndex       int    `json:"slotIndex"`
	Generation      int64  `json:"generation"`
	Revoked         bool   `json:"revoked"`
	Credential      string `json:"credential,omitempty"`
	ConnectionState string `json:"connectionState"`
}
type SlotTunnel struct {
	TunnelID        string `json:"tunnelId"`
	PublicURL       string `json:"publicUrl"`
	CustomDomain    string `json:"customDomain"`
	ProxyName       string `json:"proxyName"`
	ServerAddr      string `json:"serverAddr"`
	ServerPort      int    `json:"serverPort"`
	TransportToken  string `json:"transportToken"`
	HarnessLocalURL string `json:"harnessLocalUrl"`
}
type AcquireRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RequestID       string `json:"requestId"`
	HarnessLocalURL string `json:"harnessLocalUrl"`
}
type AcquireResponse struct {
	ProtocolVersion int               `json:"protocolVersion"`
	Entitlement     RemoteEntitlement `json:"entitlement"`
	Grant           SlotGrant         `json:"grant"`
	Tunnel          SlotTunnel        `json:"tunnel"`
}
type ReleaseRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	GrantID         string `json:"grantId"`
	SlotIndex       int    `json:"slotIndex"`
	Generation      int64  `json:"generation"`
}
type CancelRequest struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RequestID       string `json:"requestId"`
}
type StatusResponse struct {
	ProtocolVersion int               `json:"protocolVersion"`
	Device          Device            `json:"device"`
	Entitlement     RemoteEntitlement `json:"entitlement"`
	Slots           []SlotGrant       `json:"slots"`
}

var SafeReasons = map[string]bool{}

func init() {
	SafeReasons["acquire_cancelled"] = true
	safeErrorCodes["acquire_cancelled"] = true
	for _, code := range []string{"invalid_identifier", "invalid_sequence", "invalid_operation"} {
		SafeReasons[code] = true
		safeErrorCodes[code] = true
	}
	for _, v := range []string{"authorized", "service_unavailable", "plugin_secret_not_configured", "plugin_secret_invalid", "subscription_required", "subscription_expired", "grant_revoked", "grant_superseded", "device_revoked", "device_credential_invalid", "device_credential_required", "slot_over_quota", "grant_credential_invalid", "proxy_mismatch", "protocol_upgrade_required", "invalid_request", "invalid_harness_url", "request_id_conflict", "grant_key_rotated", "grant_key_not_configured", "transport_not_configured", "product_access_denied", "proxy_wait"} {
		SafeReasons[v] = true
		safeErrorCodes[v] = true
	}
}
func SafeReason(s string) string {
	if SafeReasons[s] {
		return s
	}
	return "service_unavailable"
}
func (c *Client) deviceRequest(ctx context.Context, method, path, token string, payload, out any) error {
	if token == "" {
		return errors.New("protocol_upgrade_required")
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return errors.New("invalid_request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), bytes.NewReader(body))
	if err != nil {
		return errors.New("service_unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.New("service_unavailable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errors.New("service_unavailable")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newResponseError(path, resp.StatusCode, http.StatusText(resp.StatusCode), data, true)
	}
	if json.Unmarshal(data, out) != nil {
		if path == "/api/remote/client/tunnel/cancel" {
			no := false
			return &ResponseError{StatusCode: 502, Code: "cancel_response_invalid", RetryAllowed: &no}
		}
		return errors.New("service_unavailable")
	}
	return nil
}
func (c *Client) Acquire(ctx context.Context, token string, req AcquireRequest) (*AcquireResponse, error) {
	req.ProtocolVersion = 2
	var out AcquireResponse
	err := c.deviceRequest(ctx, "POST", "/api/remote/client/tunnel/acquire", token, req, &out)
	return &out, err
}
func (c *Client) Release(ctx context.Context, token string, req ReleaseRequest) error {
	req.ProtocolVersion = 2
	var out struct {
		Released bool `json:"released"`
	}
	return c.deviceRequest(ctx, "POST", "/api/remote/client/tunnel/release", token, req, &out)
}
func (c *Client) Cancel(ctx context.Context, token string, req CancelRequest) error {
	req.ProtocolVersion = ProtocolVersion
	var out struct {
		ProtocolVersion int  `json:"protocolVersion"`
		Cancelled       bool `json:"cancelled"`
	}
	if err := c.deviceRequest(ctx, "POST", "/api/remote/client/tunnel/cancel", token, req, &out); err != nil {
		return err
	}
	if out.ProtocolVersion != ProtocolVersion || !out.Cancelled {
		no := false
		return &ResponseError{StatusCode: 502, Code: "cancel_response_invalid", RetryAllowed: &no}
	}
	return nil
}
func (c *Client) Status(ctx context.Context, token string) (*StatusResponse, error) {
	var out StatusResponse
	err := c.deviceRequest(ctx, "GET", "/api/remote/client/status", token, nil, &out)
	return &out, err
}
