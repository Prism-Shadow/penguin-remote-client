// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package util

import "testing"

func TestRemoteMachineErrorsAreAnExactFiniteGrammar(t *testing.T) {
	for _, reason := range []string{"penguin_remote:grant_revoked:stop", "penguin_remote:service_unavailable:retry", "penguin_remote:proxy_wait", "penguin_remote:invalid_sequence:stop"} {
		if !IsRemoteError(reason) {
			t.Fatal("valid protocol code hidden")
		}
	}
	for _, reason := range []string{"penguin_remote:private-secret:retry", "penguin_remote:grant_revoked:stop:private-secret", "penguin_remote:service_unavailable:maybe", "prefix penguin_remote:grant_revoked:stop"} {
		if IsRemoteError(reason) {
			t.Fatal("unrecognized error exposed")
		}
	}
}
