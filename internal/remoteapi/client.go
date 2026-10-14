package remoteapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL        = "https://go.penguin.ooo"
	defaultRequestTimeout = 15 * time.Second
)

type FlexibleString string

func (s *FlexibleString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = FlexibleString(text)
		return nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		*s = FlexibleString(number.String())
		return nil
	}
	if string(data) == "null" {
		*s = ""
		return nil
	}
	return fmt.Errorf("remoteapi flexible string: unsupported JSON value %s", string(data))
}

type Client struct {
	baseURL       *url.URL
	httpClient    *http.Client
	sessionCookie string
	origin        string
}

type ClientConfig struct {
	BaseURL       string
	SessionCookie string
	Origin        string
	HTTPClient    *http.Client
}

func NewClient(cfg ClientConfig) (*Client, error) {
	rawBaseURL := strings.TrimSpace(cfg.BaseURL)
	if rawBaseURL == "" {
		rawBaseURL = DefaultBaseURL
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return nil, errors.New("invalid Penguin Go URL")
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, errors.New("Penguin Go URL must use http or https")
	}
	if baseURL.User != nil {
		return nil, errors.New("Penguin Go URL cannot contain credentials")
	}
	if baseURL.Hostname() == "" {
		return nil, errors.New("Penguin Go URL host is required")
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("Penguin Go URL must not contain query or fragment")
	}
	if baseURL.Scheme == "http" {
		ip := net.ParseIP(baseURL.Hostname())
		if baseURL.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("Penguin Go URL must use HTTPS except on loopback")
		}
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	baseURL.RawQuery = ""
	baseURL.Fragment = ""

	origin := strings.TrimSpace(cfg.Origin)
	if origin == "" {
		origin = baseURL.Scheme + "://" + baseURL.Host
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	} else {
		copy := *httpClient
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		httpClient = &copy
	}

	return &Client{
		baseURL:       baseURL,
		httpClient:    httpClient,
		sessionCookie: normalizeSessionCookie(cfg.SessionCookie),
		origin:        origin,
	}, nil
}

func (c *Client) ClientLoginURL(returnTo string, req DeviceStartRequest, state string, clientInstanceID string) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + "/api/remote/client/login"
	q := u.Query()
	q.Set("return_to", returnTo)
	q.Set("state", state)
	q.Set("protocolVersion", "2")
	if req.DeviceName != "" {
		q.Set("device_name", req.DeviceName)
	}
	if req.Platform != "" {
		q.Set("platform", req.Platform)
	}
	if req.HarnessLocalURL != "" {
		q.Set("harness_local_url", req.HarnessLocalURL)
	}
	if clientInstanceID != "" {
		q.Set("client_instance_id", clientInstanceID)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Client) StartDevice(ctx context.Context, req DeviceStartRequest) (*Device, error) {
	if c.sessionCookie == "" {
		return nil, errors.New("Penguin Go session cookie is required for direct device start")
	}
	var device Device
	if err := c.postJSON(ctx, "/api/remote/auth/start", req, &device); err != nil {
		return nil, err
	}
	return &device, nil
}

func (c *Client) GrantTunnel(ctx context.Context, req TunnelGrantRequest) (*TunnelGrant, error) {
	if c.sessionCookie == "" {
		return nil, errors.New("Penguin Go session cookie is required for direct tunnel grant")
	}
	var grant TunnelGrant
	if err := c.postJSON(ctx, "/api/remote/tunnel/grant", req, &grant); err != nil {
		return nil, err
	}
	return &grant, nil
}

func (c *Client) ExchangeClientCode(ctx context.Context, req ClientLoginExchangeRequest) (*ClientLoginExchangeResponse, error) {
	req.ProtocolVersion = 2
	var result ClientLoginExchangeResponse
	if err := c.post(ctx, "/api/remote/client/exchange-code", req, &result, true); err != nil {
		return nil, err
	}
	return &result, nil
}

// RenewLease asks Penguin Go to extend the slot lease a running tunnel holds.
// It is redacted: the request body carries a lease id, so the response text is
// kept out of the returned error in case Penguin Go echoes it back.
func (c *Client) RenewLease(ctx context.Context, req LeaseRenewRequest) (*LeaseRenewResponse, error) {
	var result LeaseRenewResponse
	if err := c.post(ctx, LeasePath, req, &result, true); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any, out any) error {
	return c.post(ctx, path, payload, out, false)
}

// post starts the request. redact drops the response body from the error, for
// endpoints whose request carries a credential Penguin Go could echo back.
func (c *Client) post(ctx context.Context, path string, payload any, out any, redact bool) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	endpoint := c.endpoint(path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Referer", c.origin+"/")
	if c.sessionCookie != "" {
		req.Header.Set("Cookie", c.sessionCookie)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if redact {
			return errors.New("service_unavailable")
		}
		return fmt.Errorf("call Penguin Go %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Penguin Go response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newResponseError(path, resp.StatusCode, http.StatusText(resp.StatusCode), data, redact)
	}
	if err := json.Unmarshal(data, out); err != nil {
		if redact {
			return errors.New("service_unavailable")
		}
		return fmt.Errorf("decode Penguin Go %s response: %w", path, err)
	}
	return nil
}

func (c *Client) endpoint(path string) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	return u.String()
}

func normalizeSessionCookie(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "=") {
		return value
	}
	return "relay_session=" + value
}

// ResponseError is a non-2xx answer from Penguin Go. The status is carried
// separately from the text so a caller can act on a specific refusal - a lease
// that has been superseded, for instance - instead of matching on the message.
type ResponseError struct {
	RetryAllowed *bool
	Path         string
	StatusCode   int
	Status       string
	// Code is the machine-readable reason the response body carried.
	Code string
	// Message is the human-readable body text. It stays empty for endpoints
	// whose response could echo a credential back.
	Message string
}

func (e *ResponseError) Error() string {
	detail := e.Code
	if detail == "" {
		detail = e.Message
	}
	if detail == "" {
		return fmt.Sprintf("Penguin Go %s returned %s", e.Path, e.Status)
	}
	return fmt.Sprintf("Penguin Go %s returned %s: %s", e.Path, e.Status, detail)
}

// Retryable reports whether the request may be attempted again later. A server
// error, a timeout and a rate limit are worth another try; a refusal that named
// a reason is not.
func (e *ResponseError) Retryable() bool {
	if e.RetryAllowed != nil {
		return *e.RetryAllowed
	}
	switch {
	case e.StatusCode >= 500:
		return true
	case e.StatusCode == http.StatusTooManyRequests:
		return true
	case e.StatusCode == http.StatusRequestTimeout:
		return true
	default:
		return false
	}
}

// ErrorCode returns the machine-readable code a failed Penguin Go call carried,
// or an empty string when the failure had none.
func ErrorCode(err error) string {
	var responseErr *ResponseError
	if errors.As(err, &responseErr) {
		return responseErr.Code
	}
	return ""
}

// Retryable reports whether err is worth attempting again: a transport failure
// or a response that asked for a later attempt.
func Retryable(err error) bool {
	var responseErr *ResponseError
	if errors.As(err, &responseErr) {
		return responseErr.Retryable()
	}
	return true
}

// safeErrorCodes are the codes an error message may keep from an endpoint whose
// response body could echo a credential back. Anything else is dropped, because
// a message that happens to repeat a lease id would end up in the local UI and
// in the client log.
var safeErrorCodes = map[string]bool{LeaseCodeSuperseded: true}

func newResponseError(path string, statusCode int, status string, data []byte, redact bool) error {
	responseErr := &ResponseError{
		Path:       path,
		StatusCode: statusCode,
		Status:     status,
		Code:       responseErrorCode(data),
	}
	if redact {
		var flags struct {
			Retryable *bool `json:"retryable"`
		}
		if json.Unmarshal(data, &flags) == nil && safeErrorCodes[responseErr.Code] {
			responseErr.RetryAllowed = flags.Retryable
		}
		if !safeErrorCodes[responseErr.Code] {
			responseErr.Code = ""
		}
	} else {
		responseErr.Message = responseErrorText(data)
	}
	return responseErr
}

func responseErrorCode(data []byte) string {
	var body struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	if body.Code != "" {
		return body.Code
	}
	return body.Reason
}

func responseErrorText(data []byte) string {
	var body struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &body); err == nil {
		if body.Error != "" {
			return body.Error
		}
		if body.Reason != "" {
			return body.Reason
		}
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "<empty response>"
	}
	return strconv.Quote(text)
}
