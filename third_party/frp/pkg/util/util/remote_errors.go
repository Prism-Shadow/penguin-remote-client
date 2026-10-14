// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package util

var remoteCodes = map[string]bool{}

func init() {
	remoteCodes["penguin_remote:acquire_cancelled"] = true
	remoteCodes["penguin_remote:acquire_cancelled:retry"] = true
	remoteCodes["penguin_remote:acquire_cancelled:stop"] = true
	for _, code := range []string{"invalid_identifier", "invalid_sequence", "invalid_operation"} {
		remoteCodes["penguin_remote:"+code] = true
		remoteCodes["penguin_remote:"+code+":retry"] = true
		remoteCodes["penguin_remote:"+code+":stop"] = true
	}
	for _, code := range []string{"service_unavailable", "plugin_secret_not_configured", "plugin_secret_invalid", "subscription_required", "subscription_expired", "grant_revoked", "grant_superseded", "device_revoked", "device_credential_invalid", "device_credential_required", "slot_over_quota", "grant_credential_invalid", "proxy_mismatch", "protocol_upgrade_required", "invalid_request", "invalid_harness_url", "request_id_conflict", "grant_key_rotated", "grant_key_not_configured", "transport_not_configured", "product_access_denied", "proxy_wait"} {
		remoteCodes["penguin_remote:"+code] = true
		remoteCodes["penguin_remote:"+code+":retry"] = true
		remoteCodes["penguin_remote:"+code+":stop"] = true
	}
}
func IsRemoteError(s string) bool { return remoteCodes[s] }
