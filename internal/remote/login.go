package remote

import (
	"errors"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

// ConfigFromLogin converts a Penguin Go client login exchange into the local
// product config. The identity is kept for every subscription state, but the
// login stores only device identity; a slot is acquired on a separate user click.
//
// It fails closed on a response the client cannot trust, and the caller must
// leave the previously saved config untouched in that case.
func ConfigFromLogin(result *remoteapi.ClientLoginExchangeResponse, harness HarnessConfig, now time.Time) (Config, error) {
	if result == nil || result.ProtocolVersion != 2 {
		return Config{}, errors.New("protocol_upgrade_required")
	}
	return configFromIdentity(result, harness)
}

func maxConcurrentTunnels(entitlement remoteapi.RemoteEntitlement) int {
	if entitlement.MaxConcurrentTunnels == nil || *entitlement.MaxConcurrentTunnels < 0 {
		return 0
	}
	return *entitlement.MaxConcurrentTunnels
}

// UserName picks the most human-friendly name Penguin Go returned.
func UserName(user *remoteapi.RemoteUser) string {
	if user == nil {
		return ""
	}
	return firstNonEmpty(user.DisplayName, user.Username, user.Name, user.Email)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
