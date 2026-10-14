package remote

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type HarnessInstance struct {
	URL    string `json:"url"`
	Port   int    `json:"port"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

var DefaultHarnessCandidates = []HarnessInstance{
	{URL: "http://localhost:7364", Port: 7364, Kind: "desktop", Source: "PenguinHarness desktop or installed server"},
	{URL: "http://localhost:7365", Port: 7365, Kind: "pnpm-dev-web", Source: "pnpm dev:web"},
	{URL: "http://localhost:7368", Port: 7368, Kind: "pnpm-dev", Source: "pnpm dev:server"},
	{URL: "http://localhost:7369", Port: 7369, Kind: "cli-web", Source: "penguin web"},
}

func DiscoverHarness(ctx context.Context) ([]HarnessInstance, error) {
	client := &http.Client{Timeout: 900 * time.Millisecond}
	var found []HarnessInstance
	for _, candidate := range harnessCandidates() {
		ok, err := probeHarness(ctx, client, candidate.URL)
		if err == nil && ok {
			found = append(found, candidate)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no local PenguinHarness service found on ports 7364, 7365, 7368, or 7369")
	}
	return found, nil
}

func harnessCandidates() []HarnessInstance {
	candidates := desktopPortCandidates()
	seen := map[int]bool{}
	for _, candidate := range candidates {
		seen[candidate.Port] = true
	}
	for _, candidate := range DefaultHarnessCandidates {
		if !seen[candidate.Port] {
			candidates = append(candidates, candidate)
			seen[candidate.Port] = true
		}
	}
	return candidates
}

func desktopPortCandidates() []HarnessInstance {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return nil
	}
	base := filepath.Join(configDir, "PenguinHarness")
	var candidates []HarnessInstance
	seen := map[int]bool{}
	for _, filename := range []string{"server-port", "preferred-port"} {
		port, err := readPortFile(filepath.Join(base, filename))
		if err != nil {
			continue
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		candidates = append(candidates, HarnessInstance{
			URL:    fmt.Sprintf("http://localhost:%d", port),
			Port:   port,
			Kind:   "desktop",
			Source: "PenguinHarness desktop " + filename,
		})
	}
	return candidates
}

func readPortFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("port out of range: %d", port)
	}
	return port, nil
}

func probeHarness(ctx context.Context, client *http.Client, baseURL string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/", nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		return false, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return false, err
	}
	text := string(body)
	return strings.Contains(text, "PenguinHarness") || strings.Contains(text, "penguin-logo.svg"), nil
}
