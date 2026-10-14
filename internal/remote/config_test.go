package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

const (
	testActiveExpiry  = "2026-10-22T08:00:00.000Z"
	testRenewalURL    = "https://go.example.com/renew"
	testTunnelToken   = "frps-super-secret-token"
	testDeviceID      = "device-1"
	testTunnelAddress = "frps.example.com"
	// Penguin Go names the frp proxy per slot; the client never invents one.
	testProxyName = "penguin-device-1-0"
	testLeaseID   = "lease-1"
)

func TestConfigLocalBackend(t *testing.T) {
	cfg := validConfig()
	cfg.Harness.LocalURL = "http://127.0.0.1:7364"

	backend, err := cfg.LocalBackend()
	if err != nil {
		t.Fatalf("LocalBackend returned error: %v", err)
	}
	if backend.IP != "127.0.0.1" {
		t.Fatalf("backend IP = %q, want 127.0.0.1", backend.IP)
	}
	if backend.Port != 7364 {
		t.Fatalf("backend port = %d, want 7364", backend.Port)
	}
}

func TestConfigRequiresTunnelDomain(t *testing.T) {
	cfg := validConfig()
	cfg.Tunnel.CustomDomain = ""
	cfg.Tunnel.Subdomain = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded without customDomain or subdomain")
	}
}

func TestConfigRejectsHarnessPath(t *testing.T) {
	cfg := validConfig()
	cfg.Harness.LocalURL = "http://127.0.0.1:7364/harness"

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded with a non-root harness URL")
	}
}

func TestConfigRejectsNonLoopbackHarness(t *testing.T) {
	cfg := validConfig()
	cfg.Harness.LocalURL = "http://192.0.2.10:7364"

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate succeeded with a non-loopback harness URL")
	}
}

// A lapsed or unprovisioned account keeps its identity but has no tunnel, so an
// empty tunnel must not make the config itself invalid.
func TestConfigAcceptsExpiredSubscriptionWithoutTunnel(t *testing.T) {
	cfg := expiredConfig()
	cfg.Tunnel = nil

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected a tunnel-less expired config: %v", err)
	}
	if err := cfg.ValidateTunnel(); err == nil {
		t.Fatal("ValidateTunnel accepted a missing tunnel")
	}
}

func TestConfigRejectsIncompleteTunnelWhenPresent(t *testing.T) {
	cfg := activeConfig(time.Now().Add(time.Hour))
	cfg.Tunnel.Token = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a tunnel without a token")
	}
}

func TestConfigSubscriptionStates(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	t.Run("active keeps the parsed expiry", func(t *testing.T) {
		sub := activeConfig(now.Add(24 * time.Hour)).SubscriptionAt(now)
		if sub.Status != EntitlementStateActive {
			t.Fatalf("status = %q, want active", sub.Status)
		}
		if sub.ExpiresAt == nil || !sub.ExpiresAt.Equal(now.Add(24*time.Hour)) {
			t.Fatalf("expiresAt = %v, want %v", sub.ExpiresAt, now.Add(24*time.Hour))
		}
		if !sub.TunnelReady {
			t.Fatal("TunnelReady = false, want true")
		}
	})

	t.Run("expired keeps the expiry it ended at", func(t *testing.T) {
		cfg := expiredConfig()
		sub := cfg.SubscriptionAt(now)
		if sub.Status != EntitlementStateExpired {
			t.Fatalf("status = %q, want expired", sub.Status)
		}
		if sub.ExpiresAt == nil {
			t.Fatal("expiresAt = nil, want the stored expiry")
		}
		if sub.TunnelReady {
			t.Fatal("TunnelReady = true, want false for a tunnel-less expired config")
		}
	})

	t.Run("none has no expiry and no tunnel", func(t *testing.T) {
		cfg := validConfig()
		cfg.Entitlement = EntitlementConfig{Status: remoteapi.EntitlementStatusNone}
		cfg.Tunnel = nil

		sub := cfg.SubscriptionAt(now)
		if sub.Status != EntitlementStateNone {
			t.Fatalf("status = %q, want none", sub.Status)
		}
		if sub.ExpiresAt != nil {
			t.Fatalf("expiresAt = %v, want nil", sub.ExpiresAt)
		}
	})

	t.Run("unknown when no status was ever stored", func(t *testing.T) {
		cfg := validConfig()
		cfg.Entitlement = EntitlementConfig{}

		if sub := cfg.SubscriptionAt(now); sub.Status != EntitlementStateUnknown {
			t.Fatalf("status = %q, want unknown", sub.Status)
		}
	})

	t.Run("unknown when Penguin Go reported a status the client cannot read", func(t *testing.T) {
		cfg := validConfig()
		cfg.Entitlement = EntitlementConfig{Status: "trial"}

		if sub := cfg.SubscriptionAt(now); sub.Status != EntitlementStateUnknown {
			t.Fatalf("status = %q, want unknown", sub.Status)
		}
	})
}

// An active subscription whose expiry cannot be trusted must fail closed rather
// than be treated as valid forever.
func TestConfigActiveSubscriptionFailsClosedOnBadExpiry(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, expiresAt := range []string{"", "not-a-time", "2026-10-22 08:00:00"} {
		cfg := activeConfig(now.Add(time.Hour))
		cfg.Entitlement.ExpiresAt = expiresAt

		sub := cfg.SubscriptionAt(now)
		if sub.Status != EntitlementStateUnknown {
			t.Fatalf("expiresAt %q gave status %q, want unknown", expiresAt, sub.Status)
		}
		if err := cfg.TunnelStartError(now, TunnelStartProduct); err == nil {
			t.Fatalf("expiresAt %q still allowed a product tunnel start", expiresAt)
		}
	}
}

func TestConfigActiveSubscriptionExpiresAtTheDeadline(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := activeConfig(now)

	if sub := cfg.SubscriptionAt(now.Add(-time.Nanosecond)); sub.Status != EntitlementStateActive {
		t.Fatalf("status before the deadline = %q, want active", sub.Status)
	}
	for _, at := range []time.Time{now, now.Add(time.Nanosecond)} {
		if sub := cfg.SubscriptionAt(at); sub.Status != EntitlementStateExpired {
			t.Fatalf("status at %s = %q, want expired", at, sub.Status)
		}
	}
}

func TestConfigTunnelStartErrorByState(t *testing.T) {
	for _, c := range []Config{activeConfig(time.Now().Add(time.Hour)), expiredConfig(), Config{Entitlement: EntitlementConfig{Status: remoteapi.EntitlementStatusNone}}} {
		if err := c.TunnelStartError(time.Now(), TunnelStartProduct); err == nil || err.Error() != "protocol_upgrade_required" {
			t.Fatal("legacy authorization accepted")
		}
	}
}

// The developer config is only ever honoured by the CLI run command, so the
// desktop product flow cannot be unlocked by editing ordinary config fields.
func TestConfigDevConfigSourceOnlyAppliesToCLI(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	cfg := validConfig()
	cfg.Source = SourceDevConfig
	cfg.Entitlement = EntitlementConfig{}

	if err := cfg.TunnelStartError(now, TunnelStartCLI); err != nil {
		t.Fatalf("dev-config was refused by the CLI: %v", err)
	}
	if err := cfg.TunnelStartError(now, TunnelStartProduct); err == nil {
		t.Fatal("dev-config was allowed to start a tunnel in the product flow")
	}

	// A product login never bypasses the subscription, even with the same tunnel.
	cfg.Source = SourceLogin
	if err := cfg.TunnelStartError(now, TunnelStartCLI); err == nil {
		t.Fatal("a product login config skipped the subscription check in the CLI")
	}
}

func TestConfigRejectsUnsupportedVersion(t *testing.T) {
	cfg := validConfig()
	cfg.Version = DefaultConfigVersion + 1

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a future config version")
	}
}

// Version 1 configs have no subscription fields at all, so they must be read as
// unknown rather than as permanently valid.
func TestLoadConfigMigratesVersion1ToUnknownProductConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	legacy := `{
  "version": 1,
  "device": {"id": "device-1"},
  "tunnel": {
    "serverAddr": "frps.example.com",
    "serverPort": 7000,
    "token": "` + testTunnelToken + `",
    "customDomain": "remote.example.com"
  },
  "harness": {"localUrl": "http://127.0.0.1:7364"}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig rejected a version 1 config: %v", err)
	}
	if cfg.Version != DefaultConfigVersion {
		t.Fatalf("version = %d, want %d after migration", cfg.Version, DefaultConfigVersion)
	}
	if cfg.Source != SourceLogin {
		t.Fatalf("source = %q, want %q", cfg.Source, SourceLogin)
	}
	now := time.Now()
	if sub := cfg.SubscriptionAt(now); sub.Status != EntitlementStateUnknown {
		t.Fatalf("status = %q, want unknown for a legacy config", sub.Status)
	}
	if err := cfg.TunnelStartError(now, TunnelStartProduct); err == nil {
		t.Fatal("a legacy config could still start a product tunnel")
	}

	// The upgrade is only written back by an explicit save.
	saved := *cfg
	if err := SaveConfig(path, saved); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig rejected the migrated config: %v", err)
	}
	if reloaded.Version != DefaultConfigVersion {
		t.Fatalf("saved version = %d, want %d", reloaded.Version, DefaultConfigVersion)
	}
}

// Version 2 clients knew about subscriptions but did not enforce slot leases.
// Loading one upgrades it to v3 so a subsequent save is rejected by those
// older clients instead of letting them run the tunnel without renewal.
func TestLoadConfigMigratesVersion2ToLeaseAwareVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	cfg := activeConfig(time.Now().Add(time.Hour))
	cfg.Version = 2
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig rejected a version 2 config: %v", err)
	}
	if loaded.Version != DefaultConfigVersion {
		t.Fatalf("version = %d, want %d", loaded.Version, DefaultConfigVersion)
	}
	if loaded.Lease != nil || loaded.Tunnel != nil || !loaded.NeedsLogin {
		t.Fatalf("lease = %+v, want the existing lease retained during migration", loaded.Lease)
	}
}

func TestLoadConfigRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	if err := os.WriteFile(path, []byte(`{"version":0,"device":{"id":"device-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig accepted an unsupported config version")
	}
}

// A hand-edited source must not turn a product config into a developer one.
func TestConfigApplyDefaultsNormalizesUnknownSource(t *testing.T) {
	cfg := validConfig()
	cfg.Source = "hand-edited"
	cfg.ApplyDefaults()

	if cfg.Source != SourceLogin {
		t.Fatalf("source = %q, want %q", cfg.Source, SourceLogin)
	}
}

func TestConfigSaveAndLoadRoundTripWithoutTunnel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	cfg := expiredConfig()

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tunnel": null`) {
		t.Fatalf("saved config does not record a null tunnel:\n%s", data)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig rejected an expired config: %v", err)
	}
	if reloaded.Tunnel != nil {
		t.Fatalf("tunnel = %+v, want nil", reloaded.Tunnel)
	}
	if got := reloaded.SubscriptionAt(time.Now()).Status; got != EntitlementStateExpired {
		t.Fatalf("status = %q, want expired", got)
	}
}

func validConfig() Config {
	cfg := Config{
		Device: DeviceConfig{ID: testDeviceID},
		Tunnel: &TunnelConfig{
			ServerAddr:   testTunnelAddress,
			ServerPort:   7000,
			Token:        testTunnelToken,
			CustomDomain: "remote.example.com",
			ProxyName:    testProxyName,
		},
		Harness: HarnessConfig{LocalURL: DefaultHarnessURL},
	}
	cfg.ApplyDefaults()
	return cfg
}

func activeConfig(expiresAt time.Time) Config {
	cfg := validConfig()
	cfg.Entitlement = EntitlementConfig{
		Status:     remoteapi.EntitlementStatusActive,
		ExpiresAt:  expiresAt.UTC().Format(time.RFC3339Nano),
		RenewalURL: testRenewalURL,
	}
	// An active subscription always comes with a slot lease; without one the
	// client has no right to run the tunnel and refuses to start.
	cfg.Lease = activeLease(expiresAt)
	return cfg
}

// activeLease is the slot lease Penguin Go grants with an active subscription:
// it belongs to this device and does not outlive the subscription itself.
func activeLease(expiresAt time.Time) *LeaseConfig {
	slot := 0
	return &LeaseConfig{
		LeaseID:   testLeaseID,
		SlotIndex: &slot,
		DeviceID:  testDeviceID,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}
}

func expiredConfig() Config {
	cfg := validConfig()
	cfg.Entitlement = EntitlementConfig{
		Status:     remoteapi.EntitlementStatusExpired,
		ExpiresAt:  testActiveExpiry,
		RenewalURL: testRenewalURL,
	}
	cfg.Tunnel = nil
	return cfg
}

// The local page hands these links to the system handler, so only a scheme a
// Penguin Go page can legitimately use survives.
func TestSafeExternalURLKeepsOnlyOpenableLinks(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"https":           {"https://go.example.com/renew", "https://go.example.com/renew"},
		"loopback http":   {"http://127.0.0.1:3000/renew", "http://127.0.0.1:3000/renew"},
		"localhost http":  {"http://localhost:3000/renew", "http://localhost:3000/renew"},
		"remote http":     {"http://go.example.com/renew", ""},
		"file":            {"file:///etc/passwd", ""},
		"custom scheme":   {"ms-settings:privacy", ""},
		"javascript":      {"javascript:alert(1)", ""},
		"relative":        {"/renew", ""},
		"scheme relative": {"//go.example.com/renew", ""},
		"hostless":        {"https://", ""},
		"blank":           {"   ", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := safeExternalURL(tc.raw); got != tc.want {
				t.Fatalf("safeExternalURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// A config file is editable by hand, so the read path drops an unsafe renewal
// address as well.
func TestSubscriptionAtDropsAnUnsafeStoredRenewalURL(t *testing.T) {
	cfg := activeConfig(time.Now().Add(time.Hour))
	cfg.Entitlement.RenewalURL = "file:///etc/passwd"

	if got := cfg.SubscriptionAt(time.Now()).RenewalURL; got != "" {
		t.Fatalf("renewalUrl = %q, want it dropped", got)
	}
}

func TestLocalHarnessURLCannotExposeSecretsOrUnsupportedAddress(t *testing.T) {
	for _, raw := range []string{"http://user:private-secret@127.0.0.1:7364", "http://user:private-secret%ZZ@127.0.0.1:7364", "http://127.0.0.1:7364?token=private-secret", "http://127.0.0.1:7364/#private-secret", "http://127.0.0.1:0"} {
		_, err := (Config{Harness: HarnessConfig{LocalURL: raw}}).LocalBackend()
		if err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("unsafe local URL accepted or echoed")
		}
	}
}

func TestV4MigrationKeepsIdentityGrantAndPreferences(t *testing.T) {
	cfg := Config{Version: 4, Source: SourceLogin, Device: DeviceConfig{ID: "device", UserID: "1"}, DeviceAccessToken: "device-secret", Grant: &remoteapi.SlotGrant{GrantID: "grant", Credential: "grant-secret", Generation: 7, SlotIndex: 1}, Harness: HarnessConfig{LocalURL: DefaultHarnessURL, DataRoot: "custom-root"}, Desired: true, PendingRequestID: "pending"}
	cfg.migrate()
	if cfg.Version != DefaultConfigVersion || cfg.DeviceAccessToken != "device-secret" || cfg.Grant == nil || cfg.Grant.GrantID != "grant" || cfg.Harness.DataRoot != "custom-root" || cfg.PendingRequestID != "pending" || !cfg.Desired || cfg.NeedsLogin {
		t.Fatal("v5 migration discarded v4 credentials or settings")
	}
}
