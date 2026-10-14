package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClientInstanceIDIsGeneratedOnceAndReused(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "remote.json")

	first, err := LoadClientInstanceID(configPath)
	if err != nil {
		t.Fatalf("LoadClientInstanceID returned error: %v", err)
	}
	if first == "" {
		t.Fatal("the first load produced an empty id")
	}
	// 32 random bytes in URL-safe base64, without padding: long enough that it
	// cannot be guessed and safe to put in a query string.
	if len(first) != 43 {
		t.Fatalf("id = %q (%d characters), want 43", first, len(first))
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("id = %q, want URL-safe base64 without padding", first)
	}

	// The next load - a restart, or another login in the same install - reads the
	// stored id instead of making a new one.
	second, err := LoadClientInstanceID(configPath)
	if err != nil {
		t.Fatalf("second LoadClientInstanceID returned error: %v", err)
	}
	if second != first {
		t.Fatalf("id changed from %q to %q", first, second)
	}

	path, err := ClientInstanceIDPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the stored id: %v", err)
	}
	if strings.TrimSpace(string(stored)) != first {
		t.Fatalf("stored id = %q, want %q", strings.TrimSpace(string(stored)), first)
	}
	if dir := filepath.Dir(path); dir != filepath.Dir(configPath) {
		t.Fatalf("id lives in %q, want the config directory %q", dir, filepath.Dir(configPath))
	}
}

// Two installations on one machine share nothing, so neither the data directory
// nor the operating-system user may decide the id.
func TestClientInstanceIDDiffersPerInstallation(t *testing.T) {
	seen := map[string]string{}
	for i := 0; i < 4; i++ {
		configPath := filepath.Join(t.TempDir(), "remote.json")
		id, err := LoadClientInstanceID(configPath)
		if err != nil {
			t.Fatalf("LoadClientInstanceID returned error: %v", err)
		}
		if previous, ok := seen[id]; ok {
			t.Fatalf("two installations both got %q (%s)", id, previous)
		}
		seen[id] = configPath
	}
}

func TestClientInstanceIDIsNotDerivedFromTheMachine(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "remote.json")
	id, err := LoadClientInstanceID(configPath)
	if err != nil {
		t.Fatal(err)
	}
	hostname, _ := os.Hostname()
	for _, identifier := range []string{hostname, os.Getenv("USERNAME"), os.Getenv("USER")} {
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			continue
		}
		if strings.Contains(strings.ToLower(id), strings.ToLower(identifier)) {
			t.Fatalf("id %q contains the machine identifier %q", id, identifier)
		}
	}
}

// An installation that predates the id, or one whose id file was damaged, keeps
// working: a fresh id is generated and saved rather than an error being raised.
func TestClientInstanceIDRegeneratesFromAnUnusableFile(t *testing.T) {
	for name, content := range map[string]string{
		"empty":            "",
		"whitespace":       "   \n",
		"invalid runes":    "not an id at all!",
		"too long":         strings.Repeat("a", maxClientInstanceIDLength+1),
		"unexpected value": "null",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "remote.json")
			path, err := ClientInstanceIDPath(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			id, err := LoadClientInstanceID(configPath)
			if err != nil {
				t.Fatalf("LoadClientInstanceID returned error: %v", err)
			}
			if !validClientInstanceID(id) {
				t.Fatalf("id = %q, want a usable id", id)
			}
			if id == strings.TrimSpace(content) {
				t.Fatalf("id = %q, want the unusable value replaced", id)
			}

			reloaded, err := LoadClientInstanceID(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded != id {
				t.Fatalf("the replacement id was not saved: %q then %q", id, reloaded)
			}
		})
	}
}

// An install directory that does not exist yet is created, so the very first run
// of a fresh install has an id before it has a config.
func TestClientInstanceIDCreatesTheDataDirectory(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "deeper", "remote.json")

	id, err := LoadClientInstanceID(configPath)
	if err != nil {
		t.Fatalf("LoadClientInstanceID returned error: %v", err)
	}
	if id == "" {
		t.Fatal("no id was generated")
	}
	path, err := ClientInstanceIDPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the id file was not written: %v", err)
	}
}

// A generated id is useful only if it survives a restart. Failing closed here
// prevents one installation from appearing as a new device on every run.
func TestClientInstanceIDReportsPersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadClientInstanceID(filepath.Join(blocker, "remote.json"))
	if err == nil {
		t.Fatal("LoadClientInstanceID ignored a persistence failure")
	}
	if !strings.Contains(err.Error(), "save client instance id") {
		t.Fatalf("error = %q, want a persistence error", err)
	}
}

// The id file is a per-installation secret: it must not be readable by other
// users of the machine.
func TestClientInstanceIDFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not report POSIX permission bits")
	}
	configPath := filepath.Join(t.TempDir(), "remote.json")
	if _, err := LoadClientInstanceID(configPath); err != nil {
		t.Fatal(err)
	}
	path, err := ClientInstanceIDPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("id file mode = %o, want 600", mode)
	}
}

// An installation that has a config but no id - every install that predates the
// field - gets one without the config itself failing to load.
func TestClientInstanceIDMigratesAnExistingInstallation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "remote.json")

	cfg := Config{
		Version: DefaultConfigVersion,
		Source:  SourceLogin,
		Device:  DeviceConfig{ID: "device-1"},
		Entitlement: EntitlementConfig{
			Status:    "active",
			ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano),
		},
		Tunnel: &TunnelConfig{
			ServerAddr: "frps.example.com",
			ServerPort: 7000,
			Token:      "token",
			ProxyName:  "penguin-device-1-0",
			Subdomain:  "abc",
		},
		Harness: HarnessConfig{LocalURL: DefaultHarnessURL},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("write the legacy config: %v", err)
	}
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "clientInstanceId") {
		t.Fatal("the client instance id must not live in the config file")
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig rejected a config without a client instance id: %v", err)
	}
	if loaded.Device.ID != "device-1" {
		t.Fatalf("device = %+v, want the stored identity", loaded.Device)
	}

	id, err := LoadClientInstanceID(configPath)
	if err != nil {
		t.Fatalf("LoadClientInstanceID returned error: %v", err)
	}
	if !validClientInstanceID(id) {
		t.Fatalf("id = %q, want a usable id", id)
	}
	path, err := ClientInstanceIDPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the migrated id was not saved: %v", err)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("reading the instance id rewrote the config file")
	}
}

// A config file that carries an id from some future version must not be treated
// as an instance id, and must not stop the config from loading either.
func TestClientInstanceIDToleratesAnUnknownConfigShape(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "remote.json")
	raw := map[string]any{
		"version":      DefaultConfigVersion,
		"source":       SourceLogin,
		"device":       map[string]any{"id": "device-1"},
		"clientId":     "some-other-id",
		"entitlement":  map[string]any{"status": "none"},
		"harness":      map[string]any{"localUrl": DefaultHarnessURL},
		"unknownField": true,
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig rejected an unknown field: %v", err)
	}
	if loaded.Device.ID != "device-1" {
		t.Fatalf("device = %+v", loaded.Device)
	}
	id, err := LoadClientInstanceID(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if id == "some-other-id" {
		t.Fatal("the client instance id was read from an unrelated config field")
	}
}
