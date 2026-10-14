package remote

import (
	"errors"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"net/url"
	"strings"
	"time"
)

func configFromIdentity(r *remoteapi.ClientLoginExchangeResponse, h HarnessConfig) (Config, error) {
	if r.Device.DeviceID == "" || r.Device.UserID == "" || r.DeviceAccessToken == "" || !r.Entitlement.Status.Known() {
		return Config{}, errors.New("protocol_upgrade_required")
	}
	c := Config{Version: DefaultConfigVersion, Source: SourceLogin, Device: DeviceConfig{ID: r.Device.DeviceID, UserID: string(r.Device.UserID), UserName: UserName(r.User)}, DeviceAccessToken: r.DeviceAccessToken, Harness: h}
	c.ApplyEntitlement(r.Entitlement)
	c.ApplyDefaults()
	return c, c.Validate()
}
func (c *Config) ApplyEntitlement(e remoteapi.RemoteEntitlement) {
	c.Entitlement = EntitlementConfig{Status: e.Status, RenewalURL: safeExternalURL(e.RenewalURL), MaxConcurrentTunnels: maxConcurrentTunnels(e)}
	if e.ExpiresAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *e.ExpiresAt); err == nil {
			c.Entitlement.ExpiresAt = t.Format(time.RFC3339Nano)
		}
	}
}
func (c *Config) ApplyAcquire(r *remoteapi.AcquireResponse) error {
	if r == nil || r.ProtocolVersion != 2 || r.Grant.GrantID == "" || r.Grant.Credential == "" || r.Grant.Generation < 1 || r.Grant.SlotIndex < 1 || r.Grant.Revoked || r.Entitlement.Status != remoteapi.EntitlementStatusActive {
		return errors.New("invalid_request")
	}
	public, err := url.Parse(r.Tunnel.PublicURL)
	if r.Tunnel.PublicURL != "" && (err != nil || public.User != nil || (public.Scheme != "https" && public.Scheme != "http") || public.Hostname() != r.Tunnel.CustomDomain) {
		return errors.New("proxy_mismatch")
	}
	prefix := c.Device.ID + "."
	if !strings.HasPrefix(r.Tunnel.ProxyName, prefix) || r.Tunnel.CustomDomain == "" || strings.ContainsAny(r.Tunnel.CustomDomain, "/?#@") {
		return errors.New("proxy_mismatch")
	}
	next := *c
	g := r.Grant
	next.Grant = &g
	next.Tunnel = &TunnelConfig{ServerAddr: r.Tunnel.ServerAddr, ServerPort: r.Tunnel.ServerPort, Token: r.Tunnel.TransportToken, PublicURL: r.Tunnel.PublicURL, CustomDomain: r.Tunnel.CustomDomain, ProxyName: r.Tunnel.ProxyName, SlotIndex: &g.SlotIndex}
	next.Lease = nil
	next.ApplyEntitlement(r.Entitlement)
	if err := next.ValidateTunnel(); err != nil {
		return errors.New("invalid_request")
	}
	*c = next
	return nil
}
func (c Config) slotSubscription(now time.Time) Subscription {
	s := Subscription{Status: EntitlementState(c.Entitlement.Status), RenewalURL: safeExternalURL(c.Entitlement.RenewalURL), MaxConcurrentTunnels: c.Entitlement.MaxConcurrentTunnels, SlotIndex: -1, LeaseState: LeaseStateNone}
	if t, ok := c.entitlementExpiresAt(); ok {
		s.ExpiresAt = &t
		if s.Status == EntitlementStateActive && !now.Before(t) {
			s.Status = EntitlementStateUnknown
		}
	} else if s.Status == EntitlementStateActive {
		s.Status = EntitlementStateUnknown
	}
	if !c.Entitlement.Status.Known() {
		s.Status = EntitlementStateUnknown
	}
	s.TunnelReady = c.Grant != nil && !c.Grant.Revoked && c.ValidateTunnel() == nil
	if c.Grant != nil {
		s.SlotIndex = c.Grant.SlotIndex
		if s.TunnelReady {
			s.LeaseState = LeaseStateHeld
		}
	}
	return s
}
