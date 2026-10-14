// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"crypto/rand"
	"encoding/hex"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"sort"
	"strconv"
)

type RemoteConnection struct {
	ConnectionID  string   `json:"connectionId"`
	DeviceID      string   `json:"deviceId"`
	GrantID       string   `json:"grantId"`
	SlotIndex     int      `json:"slotIndex"`
	Generation    int64    `json:"generation"`
	ProxyName     string   `json:"proxyName"`
	ProxyType     string   `json:"proxyType"`
	CustomDomains []string `json:"customDomains"`
}
type RemoteSnapshot struct {
	Complete    bool               `json:"complete"`
	BootID      string             `json:"bootId"`
	Connections []RemoteConnection `json:"connections"`
}

func (pm *Manager) RemoteSnapshot() RemoteSnapshot {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := RemoteSnapshot{Complete: true, BootID: pm.remoteBoot, Connections: []RemoteConnection{}}
	for name, p := range pm.pxys {
		cfg, ok := p.GetConfigurer().(*v1.HTTPProxyConfig)
		if !ok {
			out.Complete = false
			continue
		}
		m := p.GetUserInfo().Metas
		slot, e := strconv.Atoi(m["penguin_remote_slot"])
		generation, e2 := strconv.ParseInt(m["penguin_remote_generation"], 10, 64)
		if e != nil || e2 != nil || m["penguin_remote_grant_id"] == "" || m["penguin_remote_device_id"] == "" {
			out.Complete = false
			continue
		}
		out.Connections = append(out.Connections, RemoteConnection{ConnectionID: pm.remoteIDs[name], DeviceID: m["penguin_remote_device_id"], GrantID: m["penguin_remote_grant_id"], SlotIndex: slot, Generation: generation, ProxyName: name, ProxyType: "http", CustomDomains: append([]string{}, cfg.CustomDomains...)})
	}
	sort.Slice(out.Connections, func(i, j int) bool { return out.Connections[i].ConnectionID < out.Connections[j].ConnectionID })
	if len(out.Connections) > 4096 {
		out.Complete = false
	}
	return out
}

func newRemoteBoot() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("frps lifecycle randomness unavailable")
	}
	return hex.EncodeToString(b)
}
