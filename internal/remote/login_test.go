package remote

import (
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"testing"
	"time"
)

func TestV2RejectLegacyLogin(t *testing.T) {
	if _, err := ConfigFromLogin(&remoteapi.ClientLoginExchangeResponse{Device: remoteapi.Device{DeviceID: "legacy"}}, HarnessConfig{LocalURL: DefaultHarnessURL}, time.Now()); err == nil {
		t.Fatal("legacy login accepted")
	}
}
func TestV2IdentityValidation(t *testing.T) {
	for _, r := range []*remoteapi.ClientLoginExchangeResponse{nil, {ProtocolVersion: 2, DeviceAccessToken: "secret"}, {ProtocolVersion: 2, Device: remoteapi.Device{DeviceID: "d", UserID: "1"}, Entitlement: remoteapi.RemoteEntitlement{Status: "active"}}} {
		if _, err := ConfigFromLogin(r, HarnessConfig{LocalURL: DefaultHarnessURL}, time.Now()); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}
