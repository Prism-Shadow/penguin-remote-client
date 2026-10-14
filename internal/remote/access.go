package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
)

type HarnessAccess struct {
	DataRoot string
	APIToken string
}

func ResolveHarnessAccess(ctx context.Context, localURL, configuredDataRoot string) (HarnessAccess, error) {
	roots, err := harnessDataRootCandidates(configuredDataRoot)
	if err != nil {
		return HarnessAccess{}, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	var checked []string
	for _, root := range roots {
		tokenPath := filepath.Join(root, "api-token")
		data, err := os.ReadFile(tokenPath)
		if err != nil {
			continue
		}
		checked = append(checked, root)
		token := strings.TrimSpace(string(data))
		harnessClient, err := harnessapi.NewClient(localURL, token, client)
		if err != nil {
			return HarnessAccess{}, err
		}
		if err := harnessClient.ValidateAPIToken(ctx); err == nil {
			return HarnessAccess{DataRoot: root, APIToken: token}, nil
		}
	}
	if len(checked) == 0 {
		return HarnessAccess{}, errors.New("no Harness api-token was found in the known data roots")
	}
	return HarnessAccess{}, fmt.Errorf(
		"none of the Harness api-token files matched %s (checked %s)",
		localURL,
		strings.Join(checked, ", "),
	)
}

func harnessDataRootCandidates(configured string) ([]string, error) {
	var roots []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, existing := range roots {
			if strings.EqualFold(existing, path) {
				return
			}
		}
		roots = append(roots, path)
	}
	add(configured)
	// An explicit data directory must match the selected service. Falling back
	// to another installation would hide a configuration mistake.
	if strings.TrimSpace(configured) != "" {
		return roots, nil
	}
	add(os.Getenv("PENGUIN_HOME"))
	home, err := os.UserHomeDir()
	if err != nil {
		if len(roots) > 0 {
			return roots, nil
		}
		return nil, fmt.Errorf("resolve user home for Harness data roots: %w", err)
	}
	add(filepath.Join(home, ".penguin", "data"))
	add(filepath.Join(home, ".penguin", "dev-data"))
	add(filepath.Join(home, ".penguin", "dev-data-cli"))
	return roots, nil
}
