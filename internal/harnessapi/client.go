package harnessapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const SessionCookieName = "penguin_session"

type LoginMode string

const (
	LoginAutomatic LoginMode = "automatic"
	LoginManual    LoginMode = "manual"
)

// SessionUnsupportedError is emitted only for a well-formed API response to
// the session endpoint. Authentication and transport failures never match it.
type SessionUnsupportedError struct {
	StatusCode int
}

func (e *SessionUnsupportedError) Error() string {
	return fmt.Sprintf("Harness remote session endpoint is unsupported (HTTP %d)", e.StatusCode)
}

// PrepareLogin is called after ValidateAPIToken succeeds for this endpoint.
// It probes on each new access intent. A selected mode remains fixed through
// runtime recovery; only a new intent may negotiate again.
func (c *Client) PrepareLogin(ctx context.Context, selected LoginMode) (LoginMode, RemoteSession, error) {
	if selected == LoginManual {
		return LoginManual, RemoteSession{}, nil
	}
	if selected != "" && selected != LoginAutomatic {
		return "", RemoteSession{}, errors.New("invalid Harness login mode")
	}
	session, err := c.IssueRemoteSession(ctx)
	var unsupported *SessionUnsupportedError
	if selected == "" && errors.As(err, &unsupported) {
		return LoginManual, RemoteSession{}, nil
	}
	if err != nil {
		return "", RemoteSession{}, err
	}
	return LoginAutomatic, session, nil
}

type RemoteSession struct {
	CookieName    string    `json:"cookieName"`
	SessionToken  string    `json:"sessionToken"`
	ExpiresAt     time.Time `json:"-"`
	MaxAgeSeconds int       `json:"maxAgeSeconds"`
}

type remoteSessionResponse struct {
	CookieName    string `json:"cookieName"`
	SessionToken  string `json:"sessionToken"`
	ExpiresAt     string `json:"expiresAt"`
	MaxAgeSeconds int    `json:"maxAgeSeconds"`
}

type Client struct {
	baseURL      *url.URL
	apiToken     string
	httpClient   *http.Client
	tokenMu      sync.RWMutex
	refreshMu    sync.Mutex
	tokenSource  func() (string, error)
	refreshAfter time.Time
}

func NewClient(baseURL, apiToken string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse Harness URL: %w", err)
	}
	if u.Scheme != "http" {
		return nil, errors.New("Harness URL must use http")
	}
	if u.Host == "" || !isLoopbackHost(u.Hostname()) {
		return nil, errors.New("Harness URL must point to localhost or a loopback IP")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("Harness URL must point to the service root")
	}
	if strings.TrimSpace(apiToken) == "" {
		return nil, errors.New("Harness local API token is empty")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	// Never follow an API redirect, including redirects to another loopback
	// service. Browser cookies must not influence local token validation.
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	return &Client{baseURL: u, apiToken: strings.TrimSpace(apiToken), httpClient: &client}, nil
}

func (c *Client) ValidateAPIToken(ctx context.Context) error {
	return c.validateAPIToken(ctx, c.currentAPIToken())
}

func (c *Client) validateAPIToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/api/me"), nil)
	if err != nil {
		return fmt.Errorf("create Harness API token check: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("check Harness API token: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return fmt.Errorf("read Harness API token check: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Harness API token check returned %s", resp.Status)
	}
	if err := requireJSON(resp, data); err != nil {
		return fmt.Errorf("Harness API token check: %w", err)
	}
	var body struct {
		User struct {
			UserID string `json:"userId"`
		} `json:"user"`
		SessionVia string `json:"sessionVia"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return fmt.Errorf("decode Harness API token check: %w", err)
	}
	if body.User.UserID != "admin" || body.SessionVia != "token" {
		return errors.New("Harness API token did not authenticate as the local admin token")
	}
	return nil
}

func (c *Client) IssueRemoteSession(ctx context.Context) (RemoteSession, error) {
	token := c.currentAPIToken()
	session, err := c.issueRemoteSession(ctx, token)
	var refused *sessionHTTPError
	if c.tokenSource == nil || !errors.As(err, &refused) || refused.status != http.StatusUnauthorized {
		return session, err
	}
	token, err = c.refreshAPIToken(ctx, token)
	if err != nil {
		return RemoteSession{}, err
	}
	// One retry only, even if Harness restarts again while the token is checked.
	session, err = c.issueRemoteSession(ctx, token)
	if err != nil {
		logSessionRecovery("session_retry_failed", sessionFailureReason(err))
	} else {
		logSessionRecovery("session_retry_completed", "issued")
	}
	return session, err
}

func (c *Client) issueRemoteSession(ctx context.Context, token string) (RemoteSession, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint("/api/remote/session"),
		bytes.NewReader([]byte("{}")),
	)
	if err != nil {
		return RemoteSession{}, fmt.Errorf("create Harness remote session request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return RemoteSession{}, fmt.Errorf("issue Harness remote session: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return RemoteSession{}, fmt.Errorf("read Harness remote session response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			if err := requireJSON(resp, data); err != nil {
				return RemoteSession{}, fmt.Errorf("Harness remote session response: %w", err)
			}
			return RemoteSession{}, &SessionUnsupportedError{StatusCode: resp.StatusCode}
		}
		return RemoteSession{}, &sessionHTTPError{status: resp.StatusCode}
	}
	if err := requireJSON(resp, data); err != nil {
		return RemoteSession{}, fmt.Errorf("Harness remote session response: %w", err)
	}
	var body remoteSessionResponse
	if err := json.Unmarshal(data, &body); err != nil {
		return RemoteSession{}, fmt.Errorf("decode Harness remote session response: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, body.ExpiresAt)
	if err != nil {
		return RemoteSession{}, errors.New("Harness remote session response has an invalid expiresAt")
	}
	if body.CookieName != SessionCookieName {
		return RemoteSession{}, fmt.Errorf("Harness returned unexpected session cookie name %q", body.CookieName)
	}
	if !validCookieValue(body.SessionToken) {
		return RemoteSession{}, errors.New("Harness returned an invalid remote session token")
	}
	if body.MaxAgeSeconds <= 0 || body.MaxAgeSeconds > 24*60*60 {
		return RemoteSession{}, errors.New("Harness returned an invalid remote session max age")
	}
	if !expiresAt.After(time.Now()) {
		return RemoteSession{}, errors.New("Harness returned an expired remote session")
	}
	return RemoteSession{
		CookieName:    body.CookieName,
		SessionToken:  body.SessionToken,
		ExpiresAt:     expiresAt,
		MaxAgeSeconds: body.MaxAgeSeconds,
	}, nil
}

func (c *Client) ValidateSession(ctx context.Context, token string) (bool, error) {
	if !validCookieValue(token) {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/api/me"), nil)
	if err != nil {
		return false, fmt.Errorf("create Harness session check: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("check Harness session: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
		if err != nil {
			return false, fmt.Errorf("read Harness session check: %w", err)
		}
		if err := requireJSON(resp, data); err != nil {
			return false, fmt.Errorf("Harness session check: %w", err)
		}
		var body struct {
			User struct {
				UserID string `json:"userId"`
			} `json:"user"`
		}
		if err := json.Unmarshal(data, &body); err != nil || body.User.UserID == "" {
			return false, errors.New("Harness session check has no authenticated user")
		}
		return true, nil
	case http.StatusUnauthorized:
		return false, nil
	default:
		return false, fmt.Errorf("Harness session check returned %s", resp.Status)
	}
}

func requireJSON(resp *http.Response, data []byte) error {
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("expected a Harness JSON API response")
	}
	if len(data) > 64<<10 || !json.Valid(data) || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return errors.New("invalid Harness JSON API response")
	}
	return nil
}

func (c *Client) endpoint(path string) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	return u.String()
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validCookieValue(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r >= 0x7f || r == ';' || r == ',' {
			return false
		}
	}
	return true
}
