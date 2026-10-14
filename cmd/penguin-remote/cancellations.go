package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

// Entries are written before dispatch, and removed only after a durably saved
// acquisition or acknowledged cancellation. Their secrets never enter status.
type cancelTask struct {
	BaseURL     string               `json:"baseUrl"`
	UserID      string               `json:"userId"`
	DeviceID    string               `json:"deviceId"`
	RequestID   string               `json:"requestId"`
	AccessToken string               `json:"deviceAccessToken"`
	Network     remote.NetworkConfig `json:"network"`
	Blocked     string               `json:"blocked,omitempty"`
	confirmed   bool
	attempt     int
	next        time.Time
}
type cancellationJournal struct {
	Version int           `json:"version"`
	Tasks   []*cancelTask `json:"tasks"`
}

func (a *appServer) journalPath() string { return a.configPath + ".cancellations.json" }

func ensureCLIGrantSettled(path string, c remote.Config) error {
	if c.Source == remote.SourceDevConfig {
		return nil
	}
	data, err := os.ReadFile(path + ".cancellations.json")
	if os.IsNotExist(err) {
		return nil
	}
	var journal cancellationJournal
	if err != nil || len(data) > 4<<20 || json.Unmarshal(data, &journal) != nil || journal.Version != 1 {
		return errors.New("cancel_storage_failed")
	}
	for _, task := range journal.Tasks {
		if task == nil {
			return errors.New("cancel_storage_failed")
		}
		if apiIdentity(task.BaseURL) == apiIdentity(c.PenguinGoURL) && taskDeviceMatches(task, c) {
			return errors.New("cancel_pending")
		}
	}
	return nil
}
func (a *appServer) persistConfigLocked() error {
	// Never erase the on-disk request/identity before its recovery record is durable.
	if a.cancelJournalBroken || a.cancelJournalDirty {
		a.cancelConfigDirty = true
		a.signalCancellationLocked()
		return errors.New("cancel_storage_failed")
	}
	if a.config == nil {
		a.cancelConfigDirty = false
		return nil
	}
	save := a.saveConfig
	if save == nil {
		save = remote.SaveConfig
	}
	err := save(a.configPath, *a.config)
	a.cancelConfigDirty = err != nil
	if err != nil {
		a.signalCancellationLocked()
	}
	return err
}
func (a *appServer) writeJournalLocked(tasks []*cancelTask) error {
	if a.cancelJournalBroken {
		return errors.New("cancel_storage_failed")
	}
	save := a.saveJournal
	if save == nil {
		save = remote.SavePrivateJSON
	}
	return save(a.journalPath(), cancellationJournal{Version: 1, Tasks: tasks})
}
func (a *appServer) persistJournalLocked() error {
	err := a.writeJournalLocked(a.cancellations)
	a.cancelJournalDirty = err != nil
	return err
}
func (a *appServer) sameTaskIdentity(t *cancelTask, c remote.Config) bool {
	return apiIdentity(t.BaseURL) == apiIdentity(a.penguinGoURL) && taskDeviceMatches(t, c)
}
func taskDeviceMatches(t *cancelTask, c remote.Config) bool {
	return t.DeviceID == c.Device.ID && t.UserID == c.Device.UserID
}
func apiIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = remoteapi.DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && !(u.Scheme == "https" && port == "443") && !(u.Scheme == "http" && port == "80") {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host + strings.TrimRight(u.EscapedPath(), "/")
}
func (a *appServer) taskForLocked(c remote.Config, id string) *cancelTask {
	for _, task := range a.cancellations {
		if a.sameTaskIdentity(task, c) && task.RequestID == id {
			return task
		}
	}
	return nil
}
func (a *appServer) recordRequestLocked(c remote.Config, id string) error {
	if id == "" {
		return nil
	}
	if a.taskForLocked(c, id) == nil {
		a.cancellations = append(a.cancellations, &cancelTask{BaseURL: a.penguinGoURL, UserID: c.Device.UserID, DeviceID: c.Device.ID, RequestID: id, AccessToken: c.DeviceAccessToken, Network: c.Network})
	}
	err := a.persistJournalLocked()
	a.signalCancellationLocked()
	return err
}
func (a *appServer) removeTaskLocked(task *cancelTask) error {
	next := make([]*cancelTask, 0, len(a.cancellations))
	for _, t := range a.cancellations {
		if t != task {
			next = append(next, t)
		}
	}
	if err := a.writeJournalLocked(next); err != nil {
		a.cancelJournalDirty = true
		return err
	}
	a.cancellations = next
	a.cancelJournalDirty = false
	return nil
}
func (a *appServer) cancellationBarrierLocked(i *connectionIntent) bool {
	if a.cancelJournalBroken || a.cancelJournalDirty || a.cancelConfigDirty {
		return true
	}
	for _, t := range a.cancellations {
		if taskDeviceMatches(t, *a.config) && (t.RequestID != i.requestID || !a.sameTaskIdentity(t, *a.config)) {
			return true
		}
	}
	return false
}
func (a *appServer) cancellationMessageLocked() string {
	if a.cancelJournalBroken || a.cancelJournalDirty || a.cancelConfigDirty {
		return "cancel_storage_failed"
	}
	for _, t := range a.cancellations {
		if a.config != nil && taskDeviceMatches(t, *a.config) && t.Blocked != "" {
			return t.Blocked
		}
	}
	return "cancel_pending"
}
func (a *appServer) signalCancellationLocked() {
	if a.cancelWake == nil {
		a.cancelWake = make(chan struct{}, 1)
		a.cancelContext, a.cancelShutdown = context.WithCancel(context.Background())
		go a.runCancellations(a.cancelContext)
	}
	select {
	case a.cancelWake <- struct{}{}:
	default:
	}
}
func (a *appServer) closeCancellations() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelShutdown != nil {
		a.cancelShutdown()
	}
}
func (a *appServer) handleCancelRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	a.mu.Lock()
	for _, task := range a.cancellations {
		if a.config != nil && taskDeviceMatches(task, *a.config) {
			task.Blocked = ""
			task.next = time.Time{}
		}
	}
	err := a.persistJournalLocked()
	a.wakeCancellationsLocked()
	a.mu.Unlock()
	if err != nil {
		http.Error(w, "cancel_storage_failed", 500)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
func (a *appServer) wakeCancellationsLocked() {
	for _, t := range a.cancellations {
		if t.Blocked == "" {
			t.next = time.Time{}
		}
	}
	a.signalCancellationLocked()
}
func (a *appServer) refreshCancellationCredentialLocked(c remote.Config) {
	if c.DeviceAccessToken == "" {
		return
	}
	changed := false
	for _, t := range a.cancellations {
		if a.sameTaskIdentity(t, c) && t.AccessToken != c.DeviceAccessToken {
			t.AccessToken = c.DeviceAccessToken
			t.Blocked = ""
			t.next = time.Time{}
			changed = true
		}
	}
	if changed {
		_ = a.persistJournalLocked()
	}
	a.signalCancellationLocked()
}

// Recovery records are pessimistic: after a process interruption an unresolved
// dispatched acquire is canceled, never replayed to rediscover a grant.
func (a *appServer) loadCancellations() {
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := os.ReadFile(a.journalPath())
	if err == nil {
		var journal cancellationJournal
		if len(data) > 4<<20 || json.Unmarshal(data, &journal) != nil || journal.Version != 1 || len(journal.Tasks) > 4096 {
			a.cancelJournalBroken = true
		} else {
			for _, t := range journal.Tasks {
				if t == nil || t.UserID == "" || t.DeviceID == "" || t.RequestID == "" || t.AccessToken == "" {
					a.cancelJournalBroken = true
					break
				}
				if _, err := remoteapi.NewClient(remoteapi.ClientConfig{BaseURL: t.BaseURL}); err != nil {
					a.cancelJournalBroken = true
					break
				}
				if t.Blocked != "" && t.Blocked != "cancel_auth_required" && t.Blocked != "cancel_not_supported" && t.Blocked != "cancel_rejected" {
					a.cancelJournalBroken = true
					break
				}
			}
			if !a.cancelJournalBroken {
				a.cancellations = journal.Tasks
			}
		}
	} else if !os.IsNotExist(err) {
		a.cancelJournalBroken = true
	}
	if a.config != nil {
		if a.config.PendingRequestID != "" && a.taskForLocked(*a.config, a.config.PendingRequestID) == nil && len(a.cancellations) == 0 {
			// Also fence an interrupted acquire from the preceding v4 build.
			_ = a.recordRequestLocked(*a.config, a.config.PendingRequestID)
		}
		if a.taskForLocked(*a.config, a.config.PendingRequestID) != nil || (a.config.PendingRequestID == "" && a.hasIdentityCancellationLocked(*a.config)) || a.cancelJournalBroken {
			a.config.Desired = false
			a.config.PendingRequestID = ""
			a.config.Grant = nil
			a.config.Tunnel = nil
			_ = a.persistConfigLocked()
		}
		a.refreshCancellationCredentialLocked(*a.config)
	}
	if len(a.cancellations) > 0 || a.cancelJournalBroken {
		a.signalCancellationLocked()
	}
}
func (a *appServer) hasIdentityCancellationLocked(c remote.Config) bool {
	for _, t := range a.cancellations {
		if a.sameTaskIdentity(t, c) {
			return true
		}
	}
	return false
}
func cancelFailure(err error) string {
	var response *remoteapi.ResponseError
	if errors.As(err, &response) {
		if response.StatusCode == 404 || response.StatusCode == 405 || response.StatusCode == 426 || response.Code == "cancel_response_invalid" {
			return "cancel_not_supported"
		}
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return "cancel_auth_required"
		}
		if !response.Retryable() {
			return "cancel_rejected"
		}
	}
	return ""
}
func (a *appServer) runCancellations(ctx context.Context) {
	storageAttempt := 0
	for ctx.Err() == nil {
		a.mu.Lock()
		if a.cancelJournalDirty || a.cancelConfigDirty {
			err := a.persistJournalLocked()
			if err == nil {
				err = a.persistConfigLocked()
			}
			if err != nil {
				a.mu.Unlock()
				storageAttempt++
				if !sleepContext(ctx, retryDelay(storageAttempt)) {
					return
				}
				continue
			}
		}
		storageAttempt = 0
		var selected *cancelTask
		wait := 30 * time.Second
		for _, t := range a.cancellations {
			active := a.intent != nil && a.config != nil && a.sameTaskIdentity(t, *a.config) && a.intent.requestID == t.RequestID
			if active || t.Blocked != "" {
				continue
			}
			if delay := time.Until(t.next); delay > 0 {
				if delay < wait {
					wait = delay
				}
				continue
			}
			selected = t
			break
		}
		if selected == nil {
			a.mu.Unlock()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-a.cancelWake:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		task := *selected
		network := a.systemNetwork()
		a.mu.Unlock()
		var err error
		if !task.confirmed {
			cfg := remote.Config{PenguinGoURL: apiIdentity(task.BaseURL), Device: remote.DeviceConfig{ID: task.DeviceID, UserID: task.UserID}, DeviceAccessToken: task.AccessToken, Network: network}
			cli, e := a.slotClientAt(cfg, task.BaseURL)
			err = e
			if err == nil {
				requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				err = cli.Cancel(requestCtx, task.AccessToken, remoteapi.CancelRequest{RequestID: task.RequestID})
				cancel()
			}
		}
		a.mu.Lock()
		if ctx.Err() != nil {
			a.mu.Unlock()
			return
		}
		if err == nil {
			selected.confirmed = true
			if a.removeTaskLocked(selected) != nil {
				selected.attempt++
				selected.next = time.Now().Add(retryDelay(selected.attempt))
			}
			if a.intent != nil {
				select {
				case a.intent.wake <- struct{}{}:
				default:
				}
			}
		} else if selected.AccessToken != task.AccessToken {
			selected.next = time.Time{}
		} else {
			selected.Blocked = cancelFailure(err)
			selected.attempt++
			selected.next = time.Now().Add(retryDelay(selected.attempt))
			if selected.Blocked != "" {
				_ = a.persistJournalLocked()
			}
		}
		a.mu.Unlock()
	}
}
