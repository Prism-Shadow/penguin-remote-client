package remote

import (
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"testing"
	"time"
)

func TestV2IdentityDoesNotAcquireSlot(t *testing.T) {
	for _, status := range []remoteapi.EntitlementStatus{"active", "expired", "none"} {
		r := &remoteapi.ClientLoginExchangeResponse{ProtocolVersion: 2, Device: remoteapi.Device{DeviceID: "d", UserID: "1"}, DeviceAccessToken: "device-secret", Entitlement: remoteapi.RemoteEntitlement{Status: status}}
		c, err := ConfigFromLogin(r, HarnessConfig{LocalURL: DefaultHarnessURL}, time.Now())
		if err != nil || c.Tunnel != nil || c.Grant != nil || c.DeviceAccessToken != "device-secret" {
			t.Fatal("identity login must not allocate tunnel")
		}
	}
}
func TestV2AcquireWireNameAndCachedExpiry(t *testing.T) {
	c := Config{Device: DeviceConfig{ID: "d"}, DeviceAccessToken: "device-secret", Harness: HarnessConfig{LocalURL: DefaultHarnessURL}}
	expired := "2000-01-01T00:00:00Z"
	r := &remoteapi.AcquireResponse{ProtocolVersion: 2, Entitlement: remoteapi.RemoteEntitlement{Status: "active", ExpiresAt: &expired}, Grant: remoteapi.SlotGrant{GrantID: "g", Credential: "tunnel-secret", SlotIndex: 1, Generation: 7}, Tunnel: remoteapi.SlotTunnel{ProxyName: "d.slot", CustomDomain: "u.remote.test", TransportToken: "transport", ServerAddr: "localhost", ServerPort: 7000}}
	if c.ApplyAcquire(r) != nil {
		t.Fatal("acquire rejected")
	}
	if c.SubscriptionAt(time.Now()).Status != EntitlementStateUnknown {
		t.Fatal("cached expiry must be pending")
	}
	if c.TunnelStartError(time.Now(), TunnelStartProduct) != nil {
		t.Fatal("cache stopped granted tunnel")
	}
	r.Tunnel.ProxyName = "foreign.slot"
	if c.ApplyAcquire(r) == nil {
		t.Fatal("foreign wire name accepted")
	}
}
