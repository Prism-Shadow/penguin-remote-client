package harnessapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const tokenRefreshBackoff = 2 * time.Second

type sessionHTTPError struct{ status int }

func (e *sessionHTTPError) Error() string {
	return fmt.Sprintf("Harness remote session endpoint returned %d %s", e.status, http.StatusText(e.status))
}

// NewClientWithTokenSource binds recovery to the already selected installation.
// The source must read only that installation's token; endpoint discovery is not
// repeated during recovery. A replacement is validated against this client's URL.
func NewClientWithTokenSource(baseURL, apiToken string, httpClient *http.Client, source func() (string, error)) (*Client, error) {
	c, err := NewClient(baseURL, apiToken, httpClient)
	if err != nil {
		return nil, err
	}
	c.tokenSource = source
	return c, nil
}

func (c *Client) currentAPIToken() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.apiToken
}

func (c *Client) refreshAPIToken(ctx context.Context, rejected string) (string, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if current := c.currentAPIToken(); current != rejected {
		return current, nil
	}
	if time.Now().Before(c.refreshAfter) {
		return "", errors.New("Harness API token refresh is waiting to retry")
	}
	// Start the backoff when a failed attempt completes; slow validation must
	// not consume the interval while other browser requests wait for this lock.
	refreshed := false
	defer func() {
		if !refreshed {
			c.refreshAfter = time.Now().Add(tokenRefreshBackoff)
		}
	}()
	logSessionRecovery("api_token_refresh_started", "unauthorized")
	token, err := c.tokenSource()
	if err != nil {
		logSessionRecovery("api_token_refresh_failed", "token_read_failed")
		return "", errors.New("could not read the bound Harness API token")
	}
	token = strings.TrimSpace(token)
	if token == "" || token == rejected {
		logSessionRecovery("api_token_refresh_failed", "token_not_rotated")
		return "", errors.New("the bound Harness API token has not rotated")
	}
	if err := c.validateAPIToken(ctx, token); err != nil {
		logSessionRecovery("api_token_refresh_failed", "token_validation_failed")
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("replacement Harness API token validation failed")
	}
	c.tokenMu.Lock()
	c.apiToken = token
	c.tokenMu.Unlock()
	refreshed = true
	c.refreshAfter = time.Time{}
	logSessionRecovery("api_token_refresh_completed", "validated")
	return token, nil
}

func sessionFailureReason(err error) string {
	var status *sessionHTTPError
	if errors.As(err, &status) {
		return fmt.Sprintf("http_%d", status.status)
	}
	return "request_or_response_failed"
}

// Fixed event/reason values only: never log tokens, cookies, URLs or raw errors.
func logSessionRecovery(event, reason string) {
	log.Printf("[harness] event=%s reason=%s", event, reason)
}
