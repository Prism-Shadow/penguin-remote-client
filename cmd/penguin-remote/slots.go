package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"math/rand/v2"
	"net/http"
	"time"
)

type slotClient interface {
	Acquire(context.Context, string, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error)
	Release(context.Context, string, remoteapi.ReleaseRequest) error
	Cancel(context.Context, string, remoteapi.CancelRequest) error
	Status(context.Context, string) (*remoteapi.StatusResponse, error)
}
type connectionIntent struct {
	ctx       context.Context
	cancel    context.CancelFunc
	wake      chan struct{}
	requestID string
	harness   remote.HarnessConfig
}

func cloneConfig(c remote.Config) remote.Config {
	if c.Tunnel != nil {
		v := *c.Tunnel
		c.Tunnel = &v
	}
	if c.Grant != nil {
		v := *c.Grant
		c.Grant = &v
	}
	if c.Lease != nil {
		v := *c.Lease
		c.Lease = &v
	}
	return c
}
func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
func recoveryDelay(attempt int) time.Duration {
	d := time.Second
	for i := 1; i < attempt && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
func retryDelay(attempt int) time.Duration {
	d := recoveryDelay(attempt)
	d = time.Duration(float64(d) * (0.9 + rand.Float64()*0.2))
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
func (a *appServer) slotClient(c remote.Config) (slotClient, error) {
	return a.slotClientAt(c, a.penguinGoURL)
}
func (a *appServer) slotClientAt(c remote.Config, baseURL string) (slotClient, error) {
	if !configServiceMatches(c, baseURL) {
		return nil, &remoteapi.ResponseError{StatusCode: http.StatusUnauthorized, Code: "device_credential_invalid"}
	}
	if a.newSlotClient != nil {
		return a.newSlotClient(c)
	}
	h, err := c.Network.HTTPClient()
	if err != nil {
		return nil, err
	}
	return remoteapi.NewClient(remoteapi.ClientConfig{BaseURL: baseURL, HTTPClient: h})
}
func (a *appServer) systemNetwork() remote.NetworkConfig {
	return remote.NetworkConfig{SystemProxyURL: a.systemProxyURL, SystemProxySecret: a.systemProxySecret}
}
func (a *appServer) identityClient() (*remoteapi.Client, error) {
	a.mu.Lock()
	n := a.systemNetwork()
	a.mu.Unlock()
	h, err := n.HTTPClient()
	if err != nil {
		return nil, err
	}
	return remoteapi.NewClient(remoteapi.ClientConfig{BaseURL: a.penguinGoURL, HTTPClient: h})
}
func (a *appServer) installIdentity(state string, c remote.Config) error {
	a.mu.Lock()
	if state == "" || a.loginState != state {
		a.mu.Unlock()
		return context.Canceled
	}
	a.loginState = ""
	c.PenguinGoURL = apiIdentity(a.penguinGoURL)
	old := a.config
	var release *remote.Config
	var rt *runningTunnel
	if old != nil {
		if configServiceMatches(*old, a.penguinGoURL) && old.Device.ID == c.Device.ID && old.Device.UserID == c.Device.UserID {
			c.Grant = old.Grant
			c.Tunnel = old.Tunnel
			c.Desired = old.Desired
			c.PendingRequestID = old.PendingRequestID
			c.Harness = old.Harness
			c.Device.UserName = old.Device.UserName
		} else {
			v := cloneConfig(*old)
			rt = a.detachLocked()
			if configServiceMatches(v, a.penguinGoURL) {
				release = &v
				_ = a.recordRequestLocked(v, v.PendingRequestID)
			}
		}
	}
	c.Network = a.systemNetwork()
	a.config = &c
	a.refreshCancellationCredentialLocked(c)
	var err error
	if a.cancelJournalDirty || a.cancelJournalBroken {
		err = errors.New("cancel_storage_failed")
		a.cancelConfigDirty = true
	} else {
		err = a.persistConfigLocked()
	}
	if err == nil {
		a.lastError = ""
		a.cancelConfigDirty = false
	} else {
		a.cancelConfigDirty = true
		a.signalCancellationLocked()
	}
	a.mu.Unlock()
	closeRunning(rt)
	if release != nil {
		go a.releaseConfig(*release)
	}
	return err
}
func (a *appServer) detachLocked() *runningTunnel {
	if a.intent != nil {
		a.intent.cancel()
		a.intent = nil
	}
	rt := a.tunnel
	a.tunnel = nil
	a.runnerStatus = "stopped"
	if a.config != nil {
		a.config.Desired = false
		a.config.PendingRequestID = ""
	}
	return rt
}
func closeRunning(t *runningTunnel) {
	if t != nil {
		t.cancel()
		t.runtime.Close()
	}
}
func (a *appServer) startLocked(requestID string) {
	ctx, cancel := context.WithCancel(context.Background())
	i := &connectionIntent{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), requestID: requestID, harness: a.config.Harness}
	a.intent = i
	a.config.Desired = true
	a.config.PendingRequestID = requestID
	a.runnerStatus = "confirming"
	a.lastError = ""
	go a.connectIntent(i)
}
func (a *appServer) restoreIntent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config != nil && a.config.Desired && a.config.DeviceAccessToken != "" {
		a.startLocked(a.config.PendingRequestID)
	}
}
func (a *appServer) handleTunnelStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req remote.HarnessConfig
	var local struct {
		LocalURL string `json:"localUrl"`
		DataRoot string `json:"dataRoot"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&local)
	}
	a.mu.Lock()
	if a.intent != nil {
		a.mu.Unlock()
		writeJSON(w, map[string]string{"status": "pending"})
		return
	}
	if a.config == nil || a.config.DeviceAccessToken == "" {
		a.mu.Unlock()
		http.Error(w, "protocol_upgrade_required", 403)
		return
	}
	req = a.config.Harness
	if local.LocalURL != "" {
		req.LocalURL = local.LocalURL
	}
	if local.DataRoot != "" {
		req.DataRoot = local.DataRoot
	}
	if _, err := (remote.Config{Harness: req}).LocalBackend(); err != nil {
		a.mu.Unlock()
		http.Error(w, "invalid_harness_url", 400)
		return
	}
	id, err := randomState()
	if err != nil {
		a.mu.Unlock()
		http.Error(w, "service_unavailable", 500)
		return
	}
	a.config.Harness = req
	a.startLocked(id)
	if a.persistConfigLocked() != nil {
		if a.cancelJournalDirty || a.cancelJournalBroken {
			// Preserve the queued local intent behind the storage barrier. It must
			// remain stoppable, and cannot dispatch until recovery is durable.
			a.mu.Unlock()
			writeJSON(w, map[string]string{"status": "cancel_pending"})
			return
		}
		rt := a.detachLocked()
		a.mu.Unlock()
		closeRunning(rt)
		http.Error(w, "save_failed", 500)
		return
	}
	a.mu.Unlock()
	writeJSON(w, map[string]string{"status": "confirming"})
}
func (a *appServer) handleTunnelStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	a.mu.Lock()
	var cfg *remote.Config
	if a.config != nil {
		v := cloneConfig(*a.config)
		cfg = &v
	}
	rt := a.detachLocked()
	a.lastError = ""
	if cfg != nil {
		_ = a.recordRequestLocked(*cfg, cfg.PendingRequestID)
	}
	a.signalCancellationLocked()
	if a.config != nil {
		a.config.Grant = nil
		a.config.Tunnel = nil
		if a.persistConfigLocked() != nil {
			a.lastError = "save_failed"
		}
	}
	a.mu.Unlock()
	closeRunning(rt)
	if cfg != nil {
		go a.releaseConfig(*cfg)
	}
	writeJSON(w, map[string]string{"status": "stopped"})
}
func (a *appServer) releaseConfig(c remote.Config) {
	a.mu.Lock()
	if a.config != nil && a.config.Device.ID == c.Device.ID && a.config.Device.UserID == c.Device.UserID {
		c.DeviceAccessToken = a.config.DeviceAccessToken
	}
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cli, err := a.slotClient(c)
	if err != nil {
		return
	}

	if c.Grant != nil {
		_ = cli.Release(ctx, c.DeviceAccessToken, remoteapi.ReleaseRequest{GrantID: c.Grant.GrantID, SlotIndex: c.Grant.SlotIndex, Generation: c.Grant.Generation})
	}
}
func (a *appServer) owned(i *connectionIntent) bool {
	return a.intent == i && i.ctx.Err() == nil && a.config != nil
}
func (a *appServer) waitIntent(i *connectionIntent, attempt int) bool {
	timer := time.NewTimer(retryDelay(attempt))
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-i.wake:
		return true
	case <-i.ctx.Done():
		return false
	}
}
func (a *appServer) reject(i *connectionIntent, reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.owned(i) {
		return
	}
	a.lastError = reason
	a.runnerStatus = reason
	if reason == "device_revoked" || reason == "device_credential_invalid" {
		a.config.DeviceAccessToken = ""
	}
	if reason == "subscription_expired" {
		a.config.Entitlement.Status = remoteapi.EntitlementStatusExpired
	}
	if reason == "subscription_required" {
		a.config.Entitlement.Status = remoteapi.EntitlementStatusNone
	}
	if reason != "subscription_expired" && reason != "subscription_required" && reason != "slot_over_quota" {
		a.intent = nil
		i.cancel()
		a.config.Desired = false
		a.config.PendingRequestID = ""
		a.config.Grant = nil
		a.config.Tunnel = nil
	}
	_ = a.persistConfigLocked()
	a.signalCancellationLocked()
}
func (a *appServer) connectIntent(i *connectionIntent) {
	attempt := 0
	for i.ctx.Err() == nil {
		a.mu.Lock()
		if !a.owned(i) {
			a.mu.Unlock()
			return
		}
		if a.cancellationBarrierLocked(i) {
			a.runnerStatus = "cancel_pending"
			a.lastError = a.cancellationMessageLocked()
			a.mu.Unlock()
			attempt++
			if !a.waitIntent(i, attempt) {
				return
			}
			continue
		}
		cfg := cloneConfig(*a.config)
		cfg.Harness = i.harness
		cfg.Network.SystemProxyURL = a.systemProxyURL
		cfg.Network.SystemProxySecret = a.systemProxySecret
		if i.requestID != "" && a.recordRequestLocked(cfg, i.requestID) != nil {
			a.mu.Unlock()
			a.reject(i, "cancel_storage_failed")
			return
		}
		a.mu.Unlock()
		cli, err := a.slotClient(cfg)
		if err == nil && i.requestID != "" {
			var out *remoteapi.AcquireResponse
			acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 15*time.Second)
			out, err = cli.Acquire(acquireCtx, cfg.DeviceAccessToken, remoteapi.AcquireRequest{RequestID: i.requestID, HarnessLocalURL: i.harness.LocalURL})
			acquireCancel()
			if err == nil {
				err = cfg.ApplyAcquire(out)
			}
			a.mu.Lock()
			if !a.owned(i) {
				a.mu.Unlock()
				if err == nil {
					cfg.PendingRequestID = ""
					go a.releaseConfig(cfg)
				}
				return
			}
			if err != nil && a.config.DeviceAccessToken != cfg.DeviceAccessToken {
				a.mu.Unlock()
				continue
			}
			if err == nil {
				a.config.Grant = cfg.Grant
				a.config.Tunnel = cfg.Tunnel
				a.config.ApplyEntitlement(out.Entitlement)
				a.config.PendingRequestID = ""
				a.runnerStatus = "connecting"
				a.lastError = ""
				if a.persistConfigLocked() != nil {
					err = errors.New("save_failed")
				} else if task := a.taskForLocked(cfg, i.requestID); task != nil {
					err = a.removeTaskLocked(task)
					if err != nil {
						err = errors.New("save_failed")
					}
				}
				if err == nil {
					i.requestID = ""
				}
			}
			a.mu.Unlock()
		}
		if err != nil {
			if err.Error() == "save_failed" || err.Error() == "invalid_request" || err.Error() == "proxy_mismatch" {
				a.reject(i, err.Error())
				go a.releaseConfig(cfg)
				return
			}
			if !remoteapi.Retryable(err) {
				reason := remoteapi.SafeReason(remoteapi.ErrorCode(err))
				a.reject(i, reason)
				if reason == "subscription_expired" || reason == "subscription_required" || reason == "slot_over_quota" {
					select {
					case <-i.ctx.Done():
						return
					case <-i.wake:
					}
					continue
				}
				return
			}
			a.mu.Lock()
			if a.owned(i) {
				a.lastError = "service_unavailable"
			}
			a.mu.Unlock()
			attempt++
			if !a.waitIntent(i, attempt) {
				return
			}
			continue
		}
		if cfg.Grant == nil {
			a.reject(i, "grant_revoked")
			return
		}
		rt, err := a.buildRuntime(i.ctx, cfg)
		if err != nil {
			attempt++
			a.mu.Lock()
			if a.owned(i) {
				a.lastError = "harness_unavailable"
				a.runnerStatus = "reconnecting"
			}
			a.mu.Unlock()
			if !a.waitIntent(i, attempt) {
				return
			}
			continue
		}
		ctx, cancel := context.WithCancel(i.ctx)
		t := &runningTunnel{runtime: rt, cancel: cancel}
		a.mu.Lock()
		if !a.owned(i) {
			a.mu.Unlock()
			closeRunning(t)
			return
		}
		a.tunnel = t
		a.runnerStatus = "running"
		a.authStatus = "ready"
		a.dataRoot = rt.DataRoot()
		a.lastError = ""
		a.mu.Unlock()
		err = rt.Run(ctx)
		closeRunning(t)
		a.mu.Lock()
		if !a.owned(i) {
			a.mu.Unlock()
			return
		}
		if a.tunnel == t {
			a.tunnel = nil
		}
		a.runnerStatus = "reconnecting"
		a.mu.Unlock()
		var denial interface {
			Reason() string
			Retryable() bool
		}
		if errors.As(err, &denial) && !denial.Retryable() {
			reason := denial.Reason()
			a.reject(i, reason)
			if reason == "subscription_expired" || reason == "subscription_required" || reason == "slot_over_quota" {
				select {
				case <-i.ctx.Done():
					return
				case <-i.wake:
				}
				continue
			}
			return
		}
		attempt++
		if !a.waitIntent(i, attempt) {
			return
		}
	}
}
func (a *appServer) handleSubscriptionRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	a.mu.Lock()
	if a.config == nil || a.config.DeviceAccessToken == "" {
		a.mu.Unlock()
		a.handleStatus(w, r)
		return
	}
	cfg := cloneConfig(*a.config)
	a.mu.Unlock()
	cli, err := a.slotClient(cfg)
	if err == nil {
		var out *remoteapi.StatusResponse
		out, err = cli.Status(r.Context(), cfg.DeviceAccessToken)
		if err == nil && out != nil && out.ProtocolVersion == 2 && out.Device.DeviceID == cfg.Device.ID && out.Entitlement.Status.Known() {
			a.mu.Lock()
			if a.config != nil && a.config.DeviceAccessToken == cfg.DeviceAccessToken {
				previousQuota := a.config.Entitlement.MaxConcurrentTunnels
				a.config.ApplyEntitlement(out.Entitlement)
				_ = a.persistConfigLocked()
				eligible := out.Entitlement.Status == remoteapi.EntitlementStatusActive
				if a.config.Grant != nil {
					eligible = eligible && a.config.Grant.SlotIndex <= a.config.Entitlement.MaxConcurrentTunnels
				}
				if a.runnerStatus == "slot_over_quota" && a.config.Grant == nil {
					eligible = eligible && a.config.Entitlement.MaxConcurrentTunnels > previousQuota
				}
				if a.intent != nil && (a.runnerStatus == "subscription_expired" || a.runnerStatus == "subscription_required" || a.runnerStatus == "slot_over_quota") && eligible {
					select {
					case a.intent.wake <- struct{}{}:
					default:
					}
				}
			}
			a.mu.Unlock()
		}
	}
	a.handleStatus(w, r)
}
func (a *appServer) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	a.mu.Lock()
	a.wakeCancellationsLocked()
	if a.intent != nil && a.runnerStatus != "subscription_expired" && a.runnerStatus != "subscription_required" && a.runnerStatus != "slot_over_quota" {
		select {
		case a.intent.wake <- struct{}{}:
		default:
		}
	}
	a.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}
func (a *appServer) buildRuntime(ctx context.Context, cfg remote.Config) (tunnelRuntime, error) {
	if a.newRuntime != nil {
		return a.newRuntime(ctx, cfg)
	}
	return newTunnelRuntime(ctx, cfg)
}

func (a *appServer) setLoginError(state, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state != "" && state == a.loginState {
		a.lastError = message
	}
}
