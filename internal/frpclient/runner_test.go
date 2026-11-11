package frpclient

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"

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

func TestRunnerHandlesLoginDenialsAndRetries(t *testing.T) {
	const terminal = "penguin_remote:grant_superseded:stop"
	certServer := httptest.NewTLSServer(nil)
	defer certServer.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certServer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		replies []*msg.LoginResp
	}{
		{name: "saved grant", replies: []*msg.LoginResp{{Error: terminal}}},
		{name: "temporary network failure", replies: []*msg.LoginResp{nil, {Error: terminal}}},
		{name: "retryable authorization", replies: []*msg.LoginResp{{Error: "penguin_remote:service_unavailable:retry"}, {Error: terminal}}},
		{name: "legacy generic error", replies: []*msg.LoginResp{{Error: "register control error"}, {Error: terminal}}},
		{name: "takeover after accepted login", replies: []*msg.LoginResp{{RunID: "original-control"}, {Error: terminal}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := tls.Listen("tcp", "127.0.0.1:0", certServer.TLS)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			cfg := remote.Config{
				Source:  remote.SourceDevConfig,
				Device:  remote.DeviceConfig{ID: "saved-device"},
				Harness: remote.HarnessConfig{LocalURL: remote.DefaultHarnessURL},
				Grant:   &remoteapi.SlotGrant{GrantID: "saved-grant", Credential: "saved-token", SlotIndex: 1, Generation: 7},
				Tunnel: &remote.TunnelConfig{
					ServerAddr: "127.0.0.1", ServerPort: listener.Addr().(*net.TCPAddr).Port,
					Token: "transport-token", ProxyName: "slot", CustomDomain: "remote.test", TrustedCAFile: caFile,
				},
			}
			runner, err := NewRunner(cfg)
			if err != nil {
				t.Fatal(err)
			}
			serverResult := make(chan error, 1)
			go func() {
				serverResult <- serveLoginErrors(listener, tc.replies)
			}()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err = runner.Run(ctx)
			var denial *ProtocolDenial
			if !errors.As(err, &denial) || denial.Code != "grant_superseded" || denial.Operation != "Login" || denial.Retryable() {
				t.Fatalf("Run() = %v, want terminal Login denial", err)
			}
			wantInitial := tc.replies[0] == nil || tc.replies[0].RunID == ""
			if denial.InitialLogin != wantInitial {
				t.Fatalf("initial Login denial = %v, want %v", denial.InitialLogin, wantInitial)
			}
			if err := <-serverResult; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func serveLoginErrors(listener net.Listener, replies []*msg.LoginResp) error {
	var lastAttempt time.Time
	var runID string
	for _, reply := range replies {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		now := time.Now()
		if runID == "" && !lastAttempt.IsZero() && now.Sub(lastAttempt) < 850*time.Millisecond {
			conn.Close()
			return errors.New("login retried without backoff")
		}
		lastAttempt = now
		_ = conn.SetDeadline(now.Add(3 * time.Second))
		rw := msg.NewV1ReadWriter(conn)
		var login msg.Login
		err = rw.ReadMsgInto(&login)
		if err == nil && (login.Metas["penguin_remote_grant_id"] != "saved-grant" || login.RunID != runID) {
			err = errors.New("login did not use the saved grant or existing control ID")
		}
		if err == nil && reply != nil {
			err = rw.WriteMsg(reply)
			runID = reply.RunID
		}
		conn.Close()
		if err != nil {
			return err
		}
	}
	return nil
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
	if common.Transport.DialServerKeepAlive != 60 {
		t.Fatalf("TCP keepalive idle time = %d seconds, want 60", common.Transport.DialServerKeepAlive)
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
