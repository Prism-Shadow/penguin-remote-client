package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
)

func selectionCandidates(t *testing.T, urls ...string) {
	t.Helper()
	old := remote.DefaultHarnessCandidates
	remote.DefaultHarnessCandidates = nil
	for _, url := range urls {
		remote.DefaultHarnessCandidates = append(remote.DefaultHarnessCandidates, remote.HarnessInstance{URL: url})
	}
	t.Cleanup(func() { remote.DefaultHarnessCandidates = old })
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestDiscoveryPreservesReachableSelectedHarness(t *testing.T) {
	for _, candidate := range []string{"none", "other", "localhost-alias"} {
		t.Run(candidate, func(t *testing.T) {
			selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<title>PenguinHarness selected</title>") }))
			defer selected.Close()
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<title>PenguinHarness other</title>") }))
			defer other.Close()
			switch candidate {
			case "none":
				selectionCandidates(t)
			case "other":
				selectionCandidates(t, other.URL)
			case "localhost-alias":
				selectionCandidates(t, strings.Replace(selected.URL, "127.0.0.1", "localhost", 1))
			}
			cfg := identity()
			cfg.Harness = remote.HarnessConfig{LocalURL: selected.URL, DataRoot: "selected-root"}
			a, _, _ := newTestApp(t, &cfg, "")
			a.localURL, a.dataRoot = cfg.Harness.LocalURL, cfg.Harness.DataRoot
			if err := a.refreshHarnessDiscovery(context.Background()); err != nil {
				t.Fatal(err)
			}
			if a.config.Harness != cfg.Harness || a.localURL != selected.URL || a.dataRoot != cfg.Harness.DataRoot {
				t.Fatal("reachable selected address or explicit data root was replaced")
			}
			if len(a.discovered) == 0 || a.discovered[0].URL != selected.URL {
				t.Fatal("selected custom address was not discovered first")
			}
		})
	}
}

func TestDiscoveryPreservesDesiredTargetBeforeRestore(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<title>PenguinHarness</title>") }))
	defer other.Close()
	selectionCandidates(t, other.URL)
	unavailable := httptest.NewServer(http.NotFoundHandler())
	oldURL := unavailable.URL
	unavailable.Close()
	cfg := identity()
	cfg.Desired = true
	cfg.Harness = remote.HarnessConfig{LocalURL: oldURL, DataRoot: "saved-root"}
	a, _, _ := newTestApp(t, &cfg, "")
	a.localURL, a.dataRoot = cfg.Harness.LocalURL, cfg.Harness.DataRoot
	if err := a.refreshHarnessDiscovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.config.Harness != cfg.Harness || a.localURL != oldURL || a.dataRoot != cfg.Harness.DataRoot {
		t.Fatal("discovery migrated the saved desired target before restore")
	}
	a.newSlotClient = func(remote.Config) (slotClient, error) { return &slotStub{}, nil }
	a.restoreIntent()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.intent == nil || a.intent.harness != cfg.Harness {
		t.Fatal("restart did not restore the saved target and data root")
	}
}

func TestDiscoveryDoesNotOverwriteSelectionChangedDuringProbe(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer selected.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "<title>PenguinHarness</title>") }))
	defer other.Close()
	selectionCandidates(t, other.URL)
	cfg := identity()
	cfg.Harness = remote.HarnessConfig{LocalURL: selected.URL, DataRoot: "original-root"}
	a, _, _ := newTestApp(t, &cfg, "")
	a.localURL, a.dataRoot = cfg.Harness.LocalURL, cfg.Harness.DataRoot
	done := make(chan error, 1)
	go func() { done <- a.refreshHarnessDiscovery(context.Background()) }()
	<-started
	a.mu.Lock()
	a.localURL = "http://localhost:12345"
	a.dataRoot = "new-selection-root"
	a.config.Harness = remote.HarnessConfig{LocalURL: a.localURL, DataRoot: a.dataRoot}
	newSelection := a.config.Harness
	a.mu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a.config.Harness != newSelection || a.localURL != newSelection.LocalURL || a.dataRoot != newSelection.DataRoot {
		t.Fatal("stale discovery replaced a newer explicit selection")
	}
}

func TestDiscoveryRebindsStoppedHarnessAndClearsOldRoot(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<title>PenguinHarness</title>") }))
	defer web.Close()
	selectionCandidates(t, web.URL)
	unavailable := httptest.NewServer(http.NotFoundHandler())
	oldURL := unavailable.URL
	unavailable.Close()
	cfg := identity()
	cfg.Harness = remote.HarnessConfig{LocalURL: oldURL, DataRoot: "old-installation"}
	a, _, s := newTestApp(t, &cfg, "")
	a.localURL = cfg.Harness.LocalURL
	a.dataRoot = cfg.Harness.DataRoot
	if err := a.refreshHarnessDiscovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.localURL != web.URL || a.config.Harness.LocalURL != web.URL || a.config.Harness.DataRoot != "" || a.dataRoot != "" {
		t.Fatal("discovery retained the old address or its credential directory")
	}
	stub := &slotStub{}
	a.newSlotClient = func(remote.Config) (slotClient, error) { return stub, nil }
	post(t, s, "/api/tunnel/start", "{}")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.intent == nil || a.intent.harness.LocalURL != web.URL || a.intent.harness.DataRoot != "" {
		t.Fatal("start did not use the discovered endpoint")
	}
}

func TestStartChangingHarnessDoesNotReuseAnotherRoot(t *testing.T) {
	for _, explicitRoot := range []string{"", "explicit-new-root"} {
		t.Run(explicitRoot, func(t *testing.T) {
			a, _, _, s := fixture(t)
			a.config.Harness = remote.HarnessConfig{LocalURL: "http://localhost:64222", DataRoot: "old-root"}
			a.localURL = a.config.Harness.LocalURL
			post(t, s, "/api/tunnel/start", fmt.Sprintf(`{"localUrl":"http://localhost:7365","dataRoot":%q}`, explicitRoot))
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.intent == nil || a.intent.harness.LocalURL != "http://localhost:7365" || a.intent.harness.DataRoot != explicitRoot {
				t.Fatal("start reused old credentials or ignored the explicit directory")
			}
		})
	}
}

func TestDevConfigResolvesPublicURLBeforeSaving(t *testing.T) {
	out := filepath.Join(t.TempDir(), "remote.json")
	if err := runDevConfig([]string{"-out", out, "-server-addr", "localhost", "-token", "test-token", "-domain", "public.remote.test"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := remote.LoadConfig(out)
	if err != nil || cfg.Tunnel.PublicURL != "https://public.remote.test" {
		t.Fatalf("invalid generated config: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "remote.json")
	if err := runDevConfig([]string{"-out", missing, "-server-addr", "localhost", "-token", "test-token", "-subdomain", "example"}); err == nil {
		t.Fatal("ambiguous public origin was accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("unusable config was written")
	}
}
