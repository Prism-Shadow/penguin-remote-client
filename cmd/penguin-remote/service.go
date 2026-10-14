package main

import (
	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
)

func configServiceMatches(c remote.Config, baseURL string) bool {
	return c.PenguinGoURL != "" && apiIdentity(c.PenguinGoURL) == apiIdentity(baseURL)
}

func (a *appServer) loadStoredConfig() error {
	cfg, err := remote.LoadConfig(a.configPath)
	if err != nil {
		return nil
	}
	if !configServiceMatches(*cfg, a.penguinGoURL) {
		// Unbound legacy credentials are not assigned to a guessed environment.
		a.lastError = "protocol_upgrade_required"
		return nil
	}
	if err := remote.SaveConfig(a.configPath, *cfg); err != nil {
		return err
	}
	cfg.Network = a.systemNetwork()
	a.config = cfg
	if cfg.NeedsLogin && cfg.DeviceAccessToken == "" {
		a.lastError = "protocol_upgrade_required"
	}
	a.localURL = cfg.Harness.LocalURL
	a.dataRoot = cfg.Harness.DataRoot
	return nil
}
