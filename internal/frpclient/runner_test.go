package frpclient

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

func TestRunnerClosedBeforeRunCannotStartOrPanic(t *testing.T) {
	cfg := remote.Config{Source: remote.SourceDevConfig, Device: remote.DeviceConfig{ID: "early-close"}, Harness: remote.HarnessConfig{LocalURL: remote.DefaultHarnessURL}, Tunnel: &remote.TunnelConfig{ServerAddr: "127.0.0.1", ServerPort: 7000, Token: "test-token", ProxyName: "slot", CustomDomain: "remote.test"}}
	runner, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runner.GracefulClose(time.Millisecond)
	runner.GracefulClose(time.Millisecond)
	if err := runner.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal("closed runtime was allowed to start")
	}
}

func TestBuildFRPConfigUsesAuthenticatedProxyBackend(t *testing.T) {
	cfg := remote.Config{
		Device: remote.DeviceConfig{ID: "device-1"},
		Tunnel: &remote.TunnelConfig{
			ServerAddr:   "frps.example.com",
			ServerPort:   7000,
			Token:        "frps-token",
			CustomDomain: "device.remote.example.com",
		},
		Harness: remote.HarnessConfig{LocalURL: remote.DefaultHarnessURL},
	}
	cfg.ApplyDefaults()
	if err := cfg.ValidateTunnel(); err != nil {
		t.Fatalf("ValidateTunnel returned error: %v", err)
	}

	common, proxies, err := buildFRPConfig(cfg, remote.LocalBackend{IP: "127.0.0.1", Port: 49321})
	if err != nil {
		t.Fatalf("buildFRPConfig returned error: %v", err)
	}
	if common.Transport.TCPMux == nil || *common.Transport.TCPMux {
		t.Fatal("control and work connections must use separate TCP connections")
	}
	if common.Transport.HeartbeatInterval != 10 || common.Transport.HeartbeatTimeout != 30 {
		t.Fatal("connection isolation must not weaken heartbeat checks")
	}
	proxy, ok := proxies[0].(*v1.HTTPProxyConfig)
	if !ok {
		t.Fatalf("proxy type = %T, want *v1.HTTPProxyConfig", proxies[0])
	}
	if proxy.LocalIP != "127.0.0.1" || proxy.LocalPort != 49321 {
		t.Fatalf("proxy backend = %s:%d, want 127.0.0.1:49321", proxy.LocalIP, proxy.LocalPort)
	}
	if proxy.Transport.UseCompression {
		t.Fatal("split transport must not enable tunnel compression")
	}
}

func TestBuildFRPConfigCarriesLeaseOnlyWhenGranted(t *testing.T) {
	cfg := remote.Config{
		Device: remote.DeviceConfig{ID: "device-1", UserID: "7"},
		Tunnel: &remote.TunnelConfig{
			ServerAddr: "frps.example.com", ServerPort: 7000,
			Token: "frps-token", ProxyName: "slot-1", CustomDomain: "slot.remote.example.com",
		},
		Harness: remote.HarnessConfig{LocalURL: remote.DefaultHarnessURL},
	}
	cfg.ApplyDefaults()
	common, _, err := buildFRPConfig(cfg, remote.LocalBackend{IP: "127.0.0.1", Port: 49321})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := common.Metadatas["penguin_remote_lease_id"]; ok {
		t.Fatal("a developer config without a lease carried lease metadata")
	}
	cfg.Grant = &remoteapi.SlotGrant{GrantID: "grant", Credential: "tunnel-secret", SlotIndex: 1, Generation: 7}
	cfg.DeviceAccessToken = "device-secret"
	common, _, err = buildFRPConfig(cfg, remote.LocalBackend{IP: "127.0.0.1", Port: 49321})
	if err != nil {
		t.Fatal(err)
	}
	if got := common.Metadatas["penguin_remote_credential"]; got != "tunnel-secret" {
		t.Fatalf("lease metadata = %q", got)
	}
}
