package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/frpclient"
	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
	"github.com/Prism-Shadow/penguin-remote-client/internal/localproxy"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
)

// tunnelRuntime is a running frp client plus its local authenticated proxy.
// The app talks to it through this interface so tests can drive the tunnel
// lifecycle and cancellation without a live frps.
type tunnelRuntime interface {
	Run(ctx context.Context) error
	Close()
	DataRoot() string
	ProxyConnected() bool
}

type clientRuntime struct {
	runner    *frpclient.Runner
	proxy     *localproxy.Proxy
	dataRoot  string
	loginMode harnessapi.LoginMode
	closeOnce sync.Once
}

type harnessStartupError struct {
	code string
	err  error
}

func (e *harnessStartupError) Error() string { return e.err.Error() }
func (e *harnessStartupError) Unwrap() error { return e.err }

func newClientRuntime(ctx context.Context, cfg remote.Config) (*clientRuntime, error) {
	return newClientRuntimeForMode(ctx, cfg, "")
}

func newClientRuntimeForMode(ctx context.Context, cfg remote.Config, selected harnessapi.LoginMode) (*clientRuntime, error) {
	targetBackend, err := cfg.LocalBackend()
	if err != nil {
		return nil, err
	}
	access, err := remote.ResolveHarnessAccess(ctx, cfg.Harness.LocalURL, cfg.Harness.DataRoot)
	if err != nil {
		// A stopped local service is not an authentication failure. Probe only
		// after failure; an open port never replaces the token validation above.
		conn, dialErr := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(targetBackend.IP, strconv.Itoa(targetBackend.Port)))
		if dialErr != nil {
			return nil, &harnessStartupError{code: "harness_unavailable", err: fmt.Errorf("connect to local Harness: %w", dialErr)}
		}
		_ = conn.Close()
		return nil, &harnessStartupError{code: "harness_authorization_failed", err: fmt.Errorf("resolve local Harness authorization: %w", err)}
	}
	harnessClient, err := harnessapi.NewClient(
		cfg.Harness.LocalURL,
		access.APIToken,
		&http.Client{Timeout: 5 * time.Second},
	)
	if err != nil {
		return nil, err
	}
	// ResolveHarnessAccess has authenticated the local admin token against this
	// exact endpoint. Only then may an unsupported session API select manual login.
	mode, initialSession, err := harnessClient.PrepareLogin(ctx, selected)
	if err != nil {
		return nil, &harnessStartupError{code: "harness_session_failed", err: err}
	}
	if err := cfg.ValidateTunnel(); err != nil {
		return nil, err
	}
	secure, err := publicURLUsesHTTPS(cfg.Tunnel.PublicURL)
	if err != nil {
		return nil, err
	}
	// Vite rejects the public tunnel Host before serving the pnpm development UI. Normalize
	// Host and Origin only for that known dev endpoint; packaged Harness servers keep the
	// browser-facing origin so URL generation and their own origin checks retain normal behavior.
	var proxy *localproxy.Proxy
	if mode == harnessapi.LoginManual {
		proxy, err = localproxy.StartManual(cfg.Harness.LocalURL, secure, targetBackend.Port == 7365)
	} else {
		proxy, err = localproxy.Start(cfg.Harness.LocalURL, harnessClient, secure, targetBackend.Port == 7365, initialSession)
	}
	if err != nil {
		return nil, err
	}
	ip, port := proxy.Backend()
	runner, err := frpclient.NewRunnerForBackend(cfg, remote.LocalBackend{IP: ip, Port: port})
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = proxy.Close(closeCtx)
		return nil, err
	}
	return &clientRuntime{runner: runner, proxy: proxy, dataRoot: access.DataRoot, loginMode: mode}, nil
}

func (r *clientRuntime) HarnessLoginMode() harnessapi.LoginMode { return r.loginMode }

func (r *clientRuntime) Run(ctx context.Context) error {
	return r.runner.Run(ctx)
}

func (r *clientRuntime) DataRoot() string {
	if r == nil {
		return ""
	}
	return r.dataRoot
}

func (r *clientRuntime) ProxyConnected() bool {
	return r != nil && r.runner != nil && r.runner.ProxyConnected()
}

func (r *clientRuntime) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.runner.GracefulClose(500 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = r.proxy.Close(ctx)
	})
}

func publicURLUsesHTTPS(raw string) (bool, error) {
	if raw == "" {
		return true, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false, fmt.Errorf("parse public URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		return true, nil
	case "http":
		return false, nil
	default:
		return false, fmt.Errorf("public URL must use http or https")
	}
}

func (r *clientRuntime) ConnectionPhase() string { return r.runner.ConnectionPhase() }
