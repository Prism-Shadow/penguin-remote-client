package frpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/client/proxy"
	"github.com/fatedier/frp/pkg/config"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
)

var quietOnce sync.Once

type Runner struct {
	service        *client.Service
	proxyName      string
	denial         chan *ProtocolDenial
	cancelMu       sync.Mutex
	cancel         context.CancelFunc
	protocolStatus string
}

func NewRunner(cfg remote.Config) (*Runner, error) {
	backend, err := cfg.LocalBackend()
	if err != nil {
		return nil, err
	}
	return NewRunnerForBackend(cfg, backend)
}

func NewRunnerForBackend(cfg remote.Config, backend remote.LocalBackend) (*Runner, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// A config may legitimately have no tunnel at all, so the frp client asks
	// for a complete grant explicitly before building the proxy.
	if err := cfg.ValidateTunnel(); err != nil {
		return nil, err
	}

	common, proxies, err := buildFRPConfig(cfg, backend)
	if err != nil {
		return nil, err
	}

	configSource := source.NewConfigSource()
	if err := configSource.ReplaceAll(proxies, nil); err != nil {
		return nil, fmt.Errorf("set frp config source: %w", err)
	}
	aggregator := source.NewAggregator(configSource)

	loadedProxies, visitors, err := aggregator.Load()
	if err != nil {
		return nil, fmt.Errorf("load frp config source: %w", err)
	}
	loadedProxies, visitors = config.FilterClientConfigurers(common, loadedProxies, visitors)
	loadedProxies = config.CompleteProxyConfigurers(loadedProxies)
	visitors = config.CompleteVisitorConfigurers(visitors)

	unsafeFeatures := security.NewUnsafeFeatures(nil)
	warning, err := validation.ValidateAllClientConfig(common, loadedProxies, visitors, unsafeFeatures)
	if warning != nil {
		fmt.Printf("WARNING: %v\n", warning)
	}
	if err != nil {
		return nil, fmt.Errorf("validate frp config: %w", err)
	}

	quietOnce.Do(configureDiagnosticLogging)
	reportDiagnosticSettings(common)

	r := &Runner{proxyName: strings.TrimPrefix(cfg.Tunnel.ProxyName, cfg.Device.ID+"."), denial: make(chan *ProtocolDenial, 1)}
	service, err := client.NewService(client.ServiceOptions{
		Common:                 common,
		ProtocolError:          r.protocolError,
		ConfigSourceAggregator: aggregator,
		UnsafeFeatures:         unsafeFeatures,
	})
	if err != nil {
		return nil, fmt.Errorf("create frp client service: %w", err)
	}

	r.service = service
	return r, nil
}

// ProxyConnected reports the proxy's registration state, not merely whether
// the frp service is alive and trying to reconnect.
func (r *Runner) ProxyConnected() bool {
	if r == nil || r.service == nil {
		return false
	}
	status, ok := r.service.StatusExporter().GetProxyStatus(r.proxyName)
	return ok && status != nil && status.Phase == proxy.ProxyPhaseRunning
}

func (r *Runner) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	r.cancelMu.Lock()
	r.cancel = cancel
	r.cancelMu.Unlock()
	defer cancel()
	err := r.service.Run(runCtx)
	select {
	case d := <-r.denial:
		return d
	default:
		return err
	}
}

func (r *Runner) GracefulClose(timeout time.Duration) {
	if r != nil && r.service != nil {
		r.service.GracefulClose(timeout)
	}
}

func buildFRPConfig(cfg remote.Config, backend remote.LocalBackend) (*v1.ClientCommonConfig, []v1.ProxyConfigurer, error) {
	common := &v1.ClientCommonConfig{
		ServerAddr:    cfg.Tunnel.ServerAddr,
		ServerPort:    cfg.Tunnel.ServerPort,
		User:          cfg.Device.ID,
		LoginFailExit: func() *bool { v := false; return &v }(),
		ClientID:      cfg.Device.ID,
		Auth: v1.AuthClientConfig{
			Method: "token",
			Token:  cfg.Tunnel.Token,
		},
		Metadatas: map[string]string{
			"penguin_remote_device_id": cfg.Device.ID,
			"penguin_remote_user_id":   cfg.Device.UserID,
		},
	}
	if cfg.Grant != nil {
		common.Metadatas["penguin_remote_protocol"] = "2"
		common.Metadatas["penguin_remote_grant_id"] = cfg.Grant.GrantID
		common.Metadatas["penguin_remote_credential"] = cfg.Grant.Credential
		common.Metadatas["penguin_remote_slot"] = strconv.Itoa(cfg.Grant.SlotIndex)
		common.Metadatas["penguin_remote_generation"] = strconv.FormatInt(cfg.Grant.Generation, 10)
		common.Metadatas["penguin_remote_proxy_name"] = cfg.Tunnel.ProxyName
		common.Metadatas["penguin_remote_domain"] = cfg.Tunnel.CustomDomain
	}
	common.Transport.TLS.ServerName = cfg.Tunnel.ServerAddr
	common.Transport.TLS.TrustedCaFile = cfg.Tunnel.TrustedCAFile
	// Keep control heartbeats off the TCP connections carrying Harness responses.
	common.Transport.TCPMux = func() *bool { v := false; return &v }()
	common.Transport.HeartbeatInterval = 10
	common.Transport.HeartbeatTimeout = 30
	target := (&url.URL{Scheme: "https", Host: net.JoinHostPort(cfg.Tunnel.ServerAddr, strconv.Itoa(cfg.Tunnel.ServerPort))}).String()
	p, err := cfg.Network.ProxyForURL(target)
	if err != nil {
		return nil, nil, errors.New("system_proxy_unavailable")
	}
	if p != nil {
		common.Transport.ProxyURL = p.String()
		if cfg.Network.SystemProxyURL != "" {
			common.Transport.ProxyAuthPassword = cfg.Network.SystemProxySecret
		}
	}
	common.Log.Level = "error"
	if err := common.Complete(); err != nil {
		return nil, nil, fmt.Errorf("complete frp common config: %w", err)
	}

	proxy := &v1.HTTPProxyConfig{
		ProxyBaseConfig: v1.ProxyBaseConfig{
			Name: strings.TrimPrefix(cfg.Tunnel.ProxyName, cfg.Device.ID+"."),
			Type: string(v1.ProxyTypeHTTP),
			ProxyBackend: v1.ProxyBackend{
				LocalIP:   backend.IP,
				LocalPort: backend.Port,
			},
			Metadatas: map[string]string{
				"penguin_remote_device_id": cfg.Device.ID,
			},
		},
		DomainConfig: v1.DomainConfig{
			CustomDomains: customDomains(cfg.Tunnel.CustomDomain),
			SubDomain:     cfg.Tunnel.Subdomain,
		},
	}
	proxy.Complete()

	return common, []v1.ProxyConfigurer{proxy}, nil
}

func customDomains(domain string) []string {
	if domain == "" {
		return nil
	}
	return []string{domain}
}
