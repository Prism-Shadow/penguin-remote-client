package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
)

// ResolveHarnessAPIURL separates a Vite frontend from its authenticated backend.
// A lock file is only a candidate: the exact selected API token must match there.
func ResolveHarnessAPIURL(ctx context.Context, webURL string, access HarnessAccess) (string, error) {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("probe Harness frontend: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return "", fmt.Errorf("read Harness frontend: %w", err)
	}
	if !strings.Contains(string(body), "/@vite/client") {
		return webURL, nil
	}
	data, err := os.ReadFile(filepath.Join(access.DataRoot, "server.lock"))
	var lock struct {
		Port int `json:"port"`
	}
	if err != nil || json.Unmarshal(data, &lock) != nil || lock.Port < 1 || lock.Port > 65535 {
		return "", fmt.Errorf("Vite frontend requires a valid Harness backend server.lock in the selected data directory")
	}
	selected, _ := url.Parse(webURL)
	if selected.Port() == strconv.Itoa(lock.Port) {
		return "", fmt.Errorf("Harness backend lock points to the Vite frontend")
	}
	backendURL := "http://localhost:" + strconv.Itoa(lock.Port)
	backend, err := harnessapi.NewClient(backendURL, access.APIToken, client)
	if err != nil {
		return "", err
	}
	if err := backend.ValidateAPIToken(ctx); err != nil {
		return "", fmt.Errorf("Vite backend does not match the selected Harness token: %w", err)
	}
	return backendURL, nil
}
