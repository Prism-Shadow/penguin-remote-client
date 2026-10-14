package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

const (
	// DefaultConfigVersion is the version SaveConfig writes. Version 1 predates
	// subscriptions; v4 separates device and grant credentials. v5 additionally
	// requires the cancellation journal/barrier, so a v4 client cannot resume
	// a queued new acquire while ignoring unresolved older requests.
	DefaultConfigVersion = 5
	// MinSupportedConfigVersion is the oldest config LoadConfig still accepts.
	MinSupportedConfigVersion = 1

	DefaultProxyName  = "penguin-harness"
	DefaultHarnessURL = "http://127.0.0.1:7364"

	// SourceLogin marks a config produced by a Penguin Go browser login. The
	// subscription state of these configs is always enforced.
	SourceLogin = "login"
	// SourceDevConfig marks a throwaway config written by `dev-config`. Only the
	// CLI `run` command honours it. The desktop product flow ignores the source
	// entirely, so editing ordinary config fields can never unlock remote access
	// without a valid subscription.
	SourceDevConfig = "dev-config"
)

// EntitlementState is the locally resolved Remote subscription state. It adds
// "unknown" to the states Penguin Go reports: a config written before Remote
// subscriptions existed, or one with an expiry that cannot be trusted, is
// unknown rather than assumed to be permanently valid.
type EntitlementState string

const (
	EntitlementStateActive  EntitlementState = "active"
	EntitlementStateExpired EntitlementState = "expired"
	EntitlementStateNone    EntitlementState = "none"
	EntitlementStateUnknown EntitlementState = "unknown"
)

// TunnelStartMode selects who is allowed to start a tunnel, because the
// developer `dev-config` shortcut must never reach the desktop product flow.
type TunnelStartMode int

const (
	// TunnelStartProduct is the desktop app flow. It always enforces the
	// subscription before starting.
	TunnelStartProduct TunnelStartMode = iota
	// TunnelStartCLI is `penguin-remote run`. It honours a `dev-config` file so
	// developers can test against a local frps, but product logins from a
	// Penguin Go browser login are still checked.
	TunnelStartCLI
)

// LeaseState is the locally resolved state of the slot lease that authorises a
// running tunnel. A tunnel is only allowed to run while the lease is held.
type LeaseState string

const (
	// LeaseStateHeld means a complete, unexpired lease backs the stored tunnel.
	LeaseStateHeld LeaseState = "held"
	// LeaseStateExpired means the stored lease is past its expiry and the client
	// must stop the tunnel instead of renewing into the deadline.
	LeaseStateExpired LeaseState = "expired"
	// LeaseStateSuperseded means Penguin Go handed the slot to another device.
	// The client must not ask for it back by itself; only a fresh sign-in may.
	LeaseStateSuperseded LeaseState = "superseded"
	// LeaseStateUnsupported means no lease was ever received - an older Penguin
	// Go, or a config written before the lease protocol existed. Remote access
	// stays off instead of running as if the slot had been granted forever.
	LeaseStateUnsupported LeaseState = "unsupported"
	// LeaseStateNone means no lease applies, because the subscription is not
	// active or no tunnel is stored.
	LeaseStateNone LeaseState = "none"
)

// Messages the client shows when a lease ends the tunnel. They are deliberately
// static: a lease id is a credential and never reaches a message or a log.
const (
	// LeaseSupersededMessage is the one a user is most likely to see and act on,
	// so it is written in the language the desktop page is written in. It says
	// what happened and what the user has to do; it never says which device took
	// the slot, because Penguin Go does not tell the client that.
	LeaseSupersededMessage = "远程访问已被其他设备接管，重新登录后才能再次开启"
	LeaseExpiredMessage    = "the Remote slot lease expired, remote access was stopped"
	LeaseProtocolMessage   = "Penguin Go does not support the Remote slot lease protocol yet, sign in with an updated server"
	LeaseMissingMessage    = "no Remote slot lease is stored, sign in again to use remote access"
)

type Config struct {
	// PenguinGoURL binds saved credentials and grants to their authorizing service.
	PenguinGoURL      string               `json:"penguinGoUrl,omitempty"`
	Version           int                  `json:"version"`
	DeviceAccessToken string               `json:"deviceAccessToken,omitempty"`
	Grant             *remoteapi.SlotGrant `json:"grant,omitempty"`
	Desired           bool                 `json:"desired,omitempty"`
	PendingRequestID  string               `json:"pendingRequestId,omitempty"`
	Network           NetworkConfig        `json:"network"`
	NeedsLogin        bool                 `json:"needsLogin,omitempty"`
	Source            string               `json:"source,omitempty"`
	Device            DeviceConfig         `json:"device"`
	// Entitlement is the subscription state recorded by the last login. It is
	// absent in version 1 configs, which is why those are reported as unknown.
	Entitlement EntitlementConfig `json:"entitlement"`
	// Tunnel is nil whenever no usable tunnel grant is stored, which is the
	// normal state for expired and unprovisioned subscriptions.
	Tunnel *TunnelConfig `json:"tunnel"`
	// Lease is the slot lease that authorises the stored tunnel. It is cleared
	// together with the tunnel whenever the subscription is not active, and it
	// is deliberately part of this file: the config is written with 0600, which
	// is the only place the lease id is allowed to be persisted.
	Lease *LeaseConfig `json:"lease,omitempty"`
	// LeaseSuperseded records that Penguin Go handed this installation's slot to
	// another device of the same account. It keeps the client from quietly
	// asking for the slot back, and is cleared by the next successful login.
	LeaseSuperseded bool          `json:"leaseSuperseded,omitempty"`
	Harness         HarnessConfig `json:"harness"`
}

type DeviceConfig struct {
	ID       string `json:"id"`
	UserID   string `json:"userId,omitempty"`
	UserName string `json:"userName,omitempty"`
}

// EntitlementConfig is the Remote subscription state stored by the last login.
// ExpiresAt is kept as the RFC3339Nano string Penguin Go sent so an unparsable
// value is still visible in the file instead of being silently dropped.
type EntitlementConfig struct {
	Status     remoteapi.EntitlementStatus `json:"status,omitempty"`
	ExpiresAt  string                      `json:"expiresAt,omitempty"`
	RenewalURL string                      `json:"renewalUrl,omitempty"`
	// MaxConcurrentTunnels is how many tunnels the account may run at once. Zero
	// means Penguin Go did not say.
	MaxConcurrentTunnels int `json:"maxConcurrentTunnels,omitempty"`
}

// LeaseConfig is the slot lease Penguin Go granted this installation. The lease
// id is a credential: it is stored only in this 0600 config file, and never in
// a log, an error message or a UI payload.
type LeaseConfig struct {
	LeaseID   string `json:"leaseId"`
	SlotIndex *int   `json:"slotIndex,omitempty"`
	// Generation is the fencing token that changes when the slot is handed out
	// again.
	Generation string `json:"generation,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
	// ExpiresAt is the RFC3339Nano instant the lease stops authorising the slot.
	ExpiresAt string `json:"expiresAt,omitempty"`
	RenewedAt string `json:"renewedAt,omitempty"`
}

type TunnelConfig struct {
	TrustedCAFile string `json:"trustedCaFile,omitempty"`
	ServerAddr    string `json:"serverAddr"`
	ServerPort    int    `json:"serverPort"`
	Token         string `json:"token"`
	PublicURL     string `json:"publicUrl,omitempty"`
	CustomDomain  string `json:"customDomain,omitempty"`
	Subdomain     string `json:"subdomain,omitempty"`
	// ProxyName is the name Penguin Go assigned to this tunnel. The client uses
	// it verbatim and never derives or concatenates one of its own.
	ProxyName string `json:"proxyName,omitempty"`
	// SlotIndex is the concurrency slot this tunnel occupies, as granted.
	SlotIndex *int `json:"slotIndex,omitempty"`
}

type HarnessConfig struct {
	LocalURL string `json:"localUrl"`
	DataRoot string `json:"dataRoot,omitempty"`
}

type LocalBackend struct {
	IP   string
	Port int
}

// Subscription is the locally known Remote subscription state.
type Subscription struct {
	Status EntitlementState
	// ExpiresAt is set when the stored expiry parsed and the subscription is
	// active or expired. It is nil for none and unknown.
	ExpiresAt *time.Time
	// RenewalURL is empty unless Penguin Go actually provided one.
	RenewalURL string
	// TunnelReady reports whether a complete tunnel grant is stored.
	TunnelReady bool
	// LeaseState is the state of the slot lease that authorises the tunnel.
	LeaseState LeaseState
	// LeaseExpiresAt is the instant the stored lease runs out, when known.
	LeaseExpiresAt *time.Time
	// SlotIndex is the concurrency slot this installation holds, or -1 when the
	// client does not know one.
	SlotIndex int
	// MaxConcurrentTunnels is how many tunnels the account may run at once, or 0
	// when Penguin Go did not say.
	MaxConcurrentTunnels int
}

// LeaseHeld reports whether the stored lease authorises running the tunnel.
func (s Subscription) LeaseHeld() bool {
	return s.LeaseState == LeaseStateHeld
}

// LoadConfig reads a config file and migrates older versions in memory. The
// upgrade is persisted by the next SaveConfig.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Version < MinSupportedConfigVersion || cfg.Version > DefaultConfigVersion {
		return nil, fmt.Errorf(
			"unsupported config version %d (supported %d-%d)",
			cfg.Version,
			MinSupportedConfigVersion,
			DefaultConfigVersion,
		)
	}
	cfg.migrate()
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	cfg.ApplyDefaults()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := atomicConfigWrite(path, data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// migrate upgrades an older on-disk config in memory. Version 1 had no source
// and no entitlement, so it is treated as a product login whose subscription is
// unknown: the client asks for a fresh login instead of assuming the tunnel it
// still holds is paid for.
func (c *Config) migrate() {
	if c.Version < 4 && c.Source != SourceDevConfig {
		c.DeviceAccessToken = ""
		c.Grant = nil
		c.Lease = nil
		c.Tunnel = nil
		c.Desired = false
		c.NeedsLogin = true
	}
	if c.Version < DefaultConfigVersion {
		c.Version = DefaultConfigVersion
	}
	if strings.TrimSpace(c.Source) == "" {
		c.Source = SourceLogin
	}
}

func (c *Config) ApplyDefaults() {
	if c.Version < DefaultConfigVersion {
		c.Version = DefaultConfigVersion
	}
	// Only `dev-config` unlocks the CLI developer path. Any other value,
	// including a hand-edited one, falls back to the enforced product path.
	if c.Source != SourceDevConfig {
		c.Source = SourceLogin
	}
	if strings.TrimSpace(c.Harness.LocalURL) == "" {
		c.Harness.LocalURL = DefaultHarnessURL
	}
	if c.Tunnel != nil {
		// The proxy name is used exactly as Penguin Go sends it. Only the
		// developer shortcut may fall back to the shared default, because
		// inventing a name for a product tunnel could collide with another
		// installation's proxy on the same frps.
		if strings.TrimSpace(c.Tunnel.ProxyName) == "" && c.Source == SourceDevConfig {
			c.Tunnel.ProxyName = DefaultProxyName
		}
		if c.Tunnel.ServerPort == 0 {
			c.Tunnel.ServerPort = 7000
		}
	}
}

// Validate checks the parts of a config that must hold in every subscription
// state. Tunnel completeness is checked separately because an expired or
// unprovisioned account legitimately has no tunnel at all.
func (c Config) Validate() error {
	var errs []error
	if c.Version != DefaultConfigVersion {
		errs = append(errs, fmt.Errorf("unsupported config version %d", c.Version))
	}
	if strings.TrimSpace(c.Device.ID) == "" {
		errs = append(errs, errors.New("device.id is required"))
	}
	if c.Tunnel != nil {
		if err := c.ValidateTunnel(); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := c.LocalBackend(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ValidateTunnel checks that a complete tunnel grant is stored. It is called
// only when a tunnel is actually about to be started.
func (c Config) ValidateTunnel() error {
	if c.Tunnel == nil {
		return errors.New("tunnel config is required to start remote access")
	}
	var errs []error
	if strings.TrimSpace(c.Tunnel.ServerAddr) == "" {
		errs = append(errs, errors.New("tunnel.serverAddr is required"))
	}
	if c.Tunnel.ServerPort <= 0 || c.Tunnel.ServerPort > 65535 {
		errs = append(errs, errors.New("tunnel.serverPort must be between 1 and 65535"))
	}
	if strings.TrimSpace(c.Tunnel.Token) == "" {
		errs = append(errs, errors.New("tunnel.token is required"))
	}
	if strings.TrimSpace(c.Tunnel.CustomDomain) == "" && strings.TrimSpace(c.Tunnel.Subdomain) == "" {
		errs = append(errs, errors.New("one of tunnel.customDomain or tunnel.subdomain is required"))
	}
	return errors.Join(errs...)
}

// SubscriptionAt resolves the stored subscription state at now. An active
// subscription whose expiry is missing or unparsable resolves to unknown so the
// client fails closed instead of treating it as valid forever; one whose expiry
// has passed resolves to expired.
func (c Config) SubscriptionAt(now time.Time) Subscription {
	if c.DeviceAccessToken != "" {
		return c.slotSubscription(now)
	}
	summary := Subscription{
		Status: EntitlementStateUnknown,
		// The URL is re-checked here as well as on the way in, so a hand-edited
		// config file cannot smuggle a scheme the local page would hand to the
		// operating system.
		RenewalURL:           safeExternalURL(c.Entitlement.RenewalURL),
		TunnelReady:          c.Tunnel != nil && c.ValidateTunnel() == nil,
		SlotIndex:            -1,
		MaxConcurrentTunnels: c.Entitlement.MaxConcurrentTunnels,
	}
	switch c.Entitlement.Status {
	case remoteapi.EntitlementStatusNone:
		summary.Status = EntitlementStateNone
	case remoteapi.EntitlementStatusExpired:
		summary.Status = EntitlementStateExpired
		if expiresAt, ok := c.entitlementExpiresAt(); ok {
			summary.ExpiresAt = &expiresAt
		}
	case remoteapi.EntitlementStatusActive:
		expiresAt, ok := c.entitlementExpiresAt()
		if !ok {
			return summary
		}
		summary.ExpiresAt = &expiresAt
		if !now.Before(expiresAt) {
			summary.Status = EntitlementStateExpired
			return summary
		}
		summary.Status = EntitlementStateActive
	}

	// A tunnel may only run while a lease is held, so the lease is resolved from
	// the same subscription state the caller is about to act on.
	summary.LeaseState = c.leaseStateAt(now, summary)
	if c.Lease != nil {
		if c.Lease.SlotIndex != nil {
			summary.SlotIndex = *c.Lease.SlotIndex
		} else if c.Tunnel != nil && c.Tunnel.SlotIndex != nil {
			summary.SlotIndex = *c.Tunnel.SlotIndex
		}
		if expiresAt, ok := c.leaseExpiresAt(); ok {
			summary.LeaseExpiresAt = &expiresAt
		}
	}
	return summary
}

// leaseStateAt decides whether the stored lease still authorises the tunnel the
// summary describes. Anything the client cannot confirm is reported as a state
// that keeps remote access off rather than as a usable lease.
func (c Config) leaseStateAt(now time.Time, summary Subscription) LeaseState {
	if summary.Status != EntitlementStateActive || !summary.TunnelReady {
		return LeaseStateNone
	}
	if c.LeaseSuperseded {
		return LeaseStateSuperseded
	}
	if c.Lease == nil || strings.TrimSpace(c.Lease.LeaseID) == "" {
		return LeaseStateUnsupported
	}
	// A lease that names a different device was written into the wrong config
	// file, so it is treated as unusable rather than as authorising this one.
	if c.Lease.DeviceID != "" && c.Device.ID != "" && c.Lease.DeviceID != c.Device.ID {
		return LeaseStateUnsupported
	}
	expiresAt, ok := c.leaseExpiresAt()
	if !ok {
		return LeaseStateUnsupported
	}
	if !now.Before(expiresAt) {
		return LeaseStateExpired
	}
	return LeaseStateHeld
}

// leaseExpiresAt parses the stored lease expiry as an RFC3339/ISO 8601 UTC time.
func (c Config) leaseExpiresAt() (time.Time, bool) {
	if c.Lease == nil {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(c.Lease.ExpiresAt)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

// LeaseExpiry is the instant the stored lease stops authorising the slot.
func (c Config) LeaseExpiry() (time.Time, bool) {
	return c.leaseExpiresAt()
}

// TunnelStartError reports why a tunnel must not start, or nil when it may.
func (c Config) TunnelStartError(now time.Time, mode TunnelStartMode) error {
	if mode == TunnelStartCLI && c.Source == SourceDevConfig {
		return c.ValidateTunnel()
	}
	if c.DeviceAccessToken == "" {
		return errors.New("protocol_upgrade_required")
	}
	if c.Grant == nil || c.Grant.Revoked || c.Grant.Credential == "" {
		return errors.New("grant_revoked")
	}
	if c.Tunnel == nil || !strings.HasPrefix(c.Tunnel.ProxyName, c.Device.ID+".") {
		return errors.New("proxy_mismatch")
	}
	return c.ValidateTunnel()
}

// LeaseStartError reports why the stored lease does not authorise a start. It
// fails closed on every state the client cannot confirm, so a slot is never
// occupied on the strength of a lease Penguin Go may have handed to somebody
// else.
func (c Config) LeaseStartError(subscription Subscription) error {
	switch subscription.LeaseState {
	case LeaseStateHeld:
		return nil
	case LeaseStateSuperseded:
		return errors.New(LeaseSupersededMessage)
	case LeaseStateExpired:
		return errors.New(LeaseExpiredMessage)
	case LeaseStateUnsupported:
		return errors.New(LeaseProtocolMessage)
	default:
		return errors.New(LeaseMissingMessage)
	}
}

// entitlementExpiresAt parses the stored expiry as an RFC3339/ISO 8601 UTC time.
func (c Config) entitlementExpiresAt() (time.Time, bool) {
	raw := strings.TrimSpace(c.Entitlement.ExpiresAt)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func (c Config) LocalBackend() (LocalBackend, error) {
	raw := strings.TrimSpace(c.Harness.LocalURL)
	if raw == "" {
		raw = DefaultHarnessURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return LocalBackend{}, errors.New("harness.localUrl is invalid")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return LocalBackend{}, errors.New("harness.localUrl must not contain credentials, query or fragment")
	}
	if u.Scheme != "http" {
		return LocalBackend{}, errors.New("harness.localUrl must use http for the first remote client version")
	}
	if u.Path != "" && u.Path != "/" {
		return LocalBackend{}, errors.New("harness.localUrl must point to the service root, without a path")
	}
	host := u.Hostname()
	if host == "" {
		return LocalBackend{}, errors.New("harness.localUrl host is required")
	}
	if !isLoopbackHost(host) {
		return LocalBackend{}, errors.New("harness.localUrl must point to localhost or a loopback IP")
	}
	portText := u.Port()
	if portText == "" {
		return LocalBackend{}, errors.New("harness.localUrl port is required")
	}
	_, port, err := net.SplitHostPort(net.JoinHostPort(host, portText))
	if err != nil {
		return LocalBackend{}, fmt.Errorf("harness.localUrl host/port is invalid: %w", err)
	}
	parsedPort, err := net.LookupPort("tcp", port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return LocalBackend{}, errors.New("harness.localUrl port is invalid")
	}
	return LocalBackend{IP: host, Port: parsedPort}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// safeExternalURL keeps only the URLs the local page may hand to the operating
// system. The desktop app opens an external link with the system handler, so a
// scheme Penguin Go does not legitimately use here - file:, a custom protocol
// handler, or a relative value - is dropped instead of being forwarded. Plain
// http is tolerated only for a loopback development server.
func safeExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return ""
		}
	default:
		return ""
	}
	if parsed.Hostname() == "" || parsed.User != nil {
		return ""
	}
	return raw
}
