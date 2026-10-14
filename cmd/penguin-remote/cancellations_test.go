package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
)

func cancelCount(s *slotStub) int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.cancels) }
func taskCount(a *appServer) int  { a.mu.Lock(); defer a.mu.Unlock(); return len(a.cancellations) }
func uncertainFixture(t *testing.T) (*appServer, *fakeRuntimeFactory, *slotStub, *httptest.Server) {
	a, f, stub, server := fixture(t)
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		return nil, errors.New("service_unavailable")
	}
	stub.cancelResponse = func(context.Context, string, remoteapi.CancelRequest) error { return errors.New("service_unavailable") }
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return stub.count() == 1 })
	return a, f, stub, server
}

func TestStopAndReopenWaitForDurableCancellation(t *testing.T) {
	a, factory, stub, server := fixture(t)
	entered := make(chan struct{})
	finishOld := make(chan struct{})
	var calls atomic.Int32
	var allowCancel atomic.Bool
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		out := acquired()
		n := calls.Add(1)
		out.Grant.Generation = int64(n)
		if n == 1 {
			out.Grant.GrantID = "old"
			close(entered)
			<-finishOld
		} else {
			out.Grant.GrantID = "new"
		}
		return out, nil
	}
	stub.cancelResponse = func(context.Context, string, remoteapi.CancelRequest) error {
		if !allowCancel.Load() {
			return errors.New("service_unavailable")
		}
		return nil
	}
	post(t, server, "/api/tunnel/start", "{}")
	<-entered
	post(t, server, "/api/tunnel/stop", "{}")
	if fetchStatus(t, server)["desired"] != false {
		t.Fatal("stop did not cancel local intent")
	}
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return fetchStatus(t, server)["runnerStatus"] == "cancel_pending" })
	if stub.count() != 1 || factory.latest() != nil {
		t.Fatal("R2 overtook unconfirmed R1 cancellation")
	}
	allowCancel.Store(true)
	post(t, server, "/api/wake", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	close(finishOld)
	eventually(t, func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return len(stub.release) > 0 })
	a.mu.Lock()
	grant := *a.config.Grant
	a.mu.Unlock()
	if grant.GrantID != "new" || factory.latest().closed.Load() {
		t.Fatal("late R1 response replaced R2")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.cancels) == 0 || stub.cancels[0].RequestID != stub.requests[0].RequestID || stub.requests[0].RequestID == stub.requests[1].RequestID {
		t.Fatal("cancellation was not bound to original request")
	}
	for _, release := range stub.release {
		if release.GrantID != "old" || release.Generation != 1 {
			t.Fatal("late cleanup released successor")
		}
	}
}

func TestRestartOnlyCancelsUnknownAcquireEvenWhenStopSaveFailed(t *testing.T) {
	for _, failStopSave := range []bool{false, true} {
		t.Run(fmt.Sprint(failStopSave), func(t *testing.T) {
			a, _, stub, server := uncertainFixture(t)
			if failStopSave {
				a.mu.Lock()
				a.saveConfig = func(string, remote.Config) error { return errors.New("disk unavailable") }
				a.mu.Unlock()
			}
			post(t, server, "/api/tunnel/stop", "{}")
			a.closeCancellations()
			cfg, err := remote.LoadConfig(a.configPath)
			if err != nil {
				t.Fatal(err)
			}
			restored, _, reopened := newTestApp(t, cfg, a.penguinGoURL)
			restored.configPath = a.configPath
			fresh := &slotStub{statusErr: errors.New("service_unavailable")}
			restored.newSlotClient = func(remote.Config) (slotClient, error) { return fresh, nil }
			restored.loadCancellations()
			restored.restoreIntent()
			eventually(t, func() bool { return taskCount(restored) == 0 && cancelCount(fresh) > 0 })
			post(t, reopened, "/api/wake", "{}")
			if fresh.count() != 0 || fetchStatus(t, reopened)["desired"] != false {
				t.Fatal("restart replayed stopped acquire")
			}
			stub.mu.Lock()
			original := stub.requests[0].RequestID
			stub.mu.Unlock()
			fresh.mu.Lock()
			defer fresh.mu.Unlock()
			if fresh.cancels[0].RequestID != original {
				t.Fatal("restart lost original cancellation ID")
			}
		})
	}
}

func TestCancellationPermanentFailureIsRetainedUntilCredentialRotation(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	stub.mu.Lock()
	stub.cancelResponse = func(_ context.Context, token string, _ remoteapi.CancelRequest) error {
		if token == "rotated-device-secret" {
			return nil
		}
		return &remoteapi.ResponseError{StatusCode: 401, Code: "device_credential_invalid"}
	}
	stub.mu.Unlock()
	post(t, server, "/api/tunnel/stop", "{}")
	eventually(t, func() bool { return fetchStatus(t, server)["lastError"] == "cancel_auth_required" })
	count := cancelCount(stub)
	time.Sleep(100 * time.Millisecond)
	if cancelCount(stub) != count || taskCount(a) != 1 {
		t.Fatal("permanent refusal retried or discarded task")
	}
	a.mu.Lock()
	a.loginState = "rotation"
	a.mu.Unlock()
	c := identity()
	c.DeviceAccessToken = "rotated-device-secret"
	if err := a.installIdentity("rotation", c); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return taskCount(a) == 0 })
	if stub.count() != 1 {
		t.Fatal("credential rotation reacquired")
	}
}

func TestAccountSwitchNeverUsesNewIdentityForOldCancellation(t *testing.T) {
	a, factory, stub, server := uncertainFixture(t)
	var wrong atomic.Bool
	stub.mu.Lock()
	stub.cancelResponse = func(_ context.Context, token string, _ remoteapi.CancelRequest) error {
		if token != "device-secret" {
			wrong.Store(true)
		}
		return &remoteapi.ResponseError{StatusCode: 401, Code: "device_credential_invalid"}
	}
	stub.mu.Unlock()
	a.mu.Lock()
	a.loginState = "other"
	a.mu.Unlock()
	c := identity()
	c.Device.ID = "other-device"
	c.Device.UserID = "2"
	c.DeviceAccessToken = "other-secret"
	if err := a.installIdentity("other", c); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return cancelCount(stub) > 0 })
	stub.mu.Lock()
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		out := acquired()
		out.Tunnel.ProxyName = "other-device.slot"
		return out, nil
	}
	stub.mu.Unlock()
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	if wrong.Load() || taskCount(a) != 1 {
		t.Fatal("old task mixed with new account")
	}
}

func TestCancellationStorageFailuresBlockAllNewAcquires(t *testing.T) {
	a, factory, stub, server := fixture(t)
	var fail atomic.Bool
	fail.Store(true)
	a.saveJournal = func(path string, value any) error {
		if fail.Load() {
			return errors.New("disk unavailable")
		}
		return remote.SavePrivateJSON(path, value)
	}
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return fetchStatus(t, server)["lastError"] == "cancel_storage_failed" })
	post(t, server, "/api/tunnel/start", "{}")
	time.Sleep(100 * time.Millisecond)
	if stub.count() != 0 || factory.latest() != nil || taskCount(a) != 1 {
		t.Fatal("failed write-ahead storage bypassed acquire barrier")
	}
	fail.Store(false)
	post(t, server, "/api/wake", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	if cancelCount(stub) == 0 || stub.count() != 1 {
		t.Fatal("recovered storage did not settle old task first")
	}
}

func TestConfirmedCancelIsNotForgottenWhenJournalRemovalFails(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	var failRemoval atomic.Bool
	failRemoval.Store(true)
	a.mu.Lock()
	a.saveJournal = func(path string, value any) error {
		if len(value.(cancellationJournal).Tasks) == 0 && failRemoval.Load() {
			return errors.New("disk unavailable")
		}
		return remote.SavePrivateJSON(path, value)
	}
	a.mu.Unlock()
	stub.mu.Lock()
	stub.cancelResponse = func(context.Context, string, remoteapi.CancelRequest) error { return nil }
	stub.mu.Unlock()
	post(t, server, "/api/tunnel/stop", "{}")
	eventually(t, func() bool { return cancelCount(stub) > 0 })
	post(t, server, "/api/tunnel/start", "{}")
	time.Sleep(100 * time.Millisecond)
	if stub.count() != 1 || taskCount(a) != 1 {
		t.Fatal("failed durable task removal opened barrier")
	}
	failRemoval.Store(false)
	post(t, server, "/api/wake", "{}")
	eventually(t, func() bool { return stub.count() > 1 })
}

func TestMissingCancelEndpointBlocksWithoutNetworkRetryOrSecrets(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	stub.mu.Lock()
	stub.cancelResponse = func(context.Context, string, remoteapi.CancelRequest) error {
		return &remoteapi.ResponseError{StatusCode: 404}
	}
	stub.mu.Unlock()
	post(t, server, "/api/tunnel/stop", "{}")
	eventually(t, func() bool { return fetchStatus(t, server)["lastError"] == "cancel_not_supported" })
	before := cancelCount(stub)
	post(t, server, "/api/tunnel/start", "{}")
	post(t, server, "/api/wake", "{}")
	time.Sleep(100 * time.Millisecond)
	if cancelCount(stub) != before || stub.count() != 1 || taskCount(a) != 1 {
		t.Fatal("missing contract bypassed barrier or retried as network failure")
	}
	data, _ := json.Marshal(fetchStatus(t, server))
	if strings.Contains(string(data), "device-secret") || strings.Contains(string(data), stub.requests[0].RequestID) {
		t.Fatal("cancellation status exposed private fields")
	}
}

func TestCorruptCancellationJournalIsPreservedAndFailsClosed(t *testing.T) {
	a, _, stub, server := fixture(t)
	data := []byte("invalid private journal")
	if err := os.WriteFile(a.journalPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	a.loadCancellations()
	post(t, server, "/api/tunnel/start", "{}")
	time.Sleep(100 * time.Millisecond)
	read, _ := os.ReadFile(a.journalPath())
	if string(read) != string(data) || stub.count() != 0 || fetchStatus(t, server)["lastError"] != "cancel_storage_failed" {
		t.Fatal("corrupt journal was discarded or ignored")
	}
}

func TestStopWhileWaitingCancelsQueuedIntentWithoutAcquire(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	post(t, server, "/api/tunnel/stop", "{}")
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return fetchStatus(t, server)["runnerStatus"] == "cancel_pending" })
	post(t, server, "/api/tunnel/stop", "{}")
	stub.mu.Lock()
	stub.cancelResponse = func(context.Context, string, remoteapi.CancelRequest) error { return nil }
	stub.mu.Unlock()
	post(t, server, "/api/wake", "{}")
	eventually(t, func() bool { return taskCount(a) == 0 })
	if stub.count() != 1 || fetchStatus(t, server)["desired"] != false {
		t.Fatal("stopped queued R2 acquired after cancellation recovered")
	}
}

func TestRestartPreservesQueuedNewIntentBehindOldCancellation(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	post(t, server, "/api/tunnel/stop", "{}")
	post(t, server, "/api/tunnel/start", "{}")
	a.mu.Lock()
	pending := a.config.PendingRequestID
	a.mu.Unlock()
	a.closeCancellations()
	a.mu.Lock()
	rt := a.detachLocked()
	a.mu.Unlock()
	closeRunning(rt)
	cfg, err := remote.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	restored, factory, reopened := newTestApp(t, cfg, a.penguinGoURL)
	restored.configPath = a.configPath
	fresh := &slotStub{}
	restored.newSlotClient = func(remote.Config) (slotClient, error) { return fresh, nil }
	restored.loadCancellations()
	restored.restoreIntent()
	eventually(t, func() bool { return factory.latest() != nil })
	fresh.mu.Lock()
	defer fresh.mu.Unlock()
	stub.mu.Lock()
	original := stub.requests[0].RequestID
	stub.mu.Unlock()
	if len(fresh.cancels) != 1 || fresh.cancels[0].RequestID != original || len(fresh.requests) != 1 || fresh.requests[0].RequestID != pending || fetchStatus(t, reopened)["desired"] != true {
		t.Fatal("restart lost queued intent or bypassed old cancellation")
	}
}

func TestLateCancellationAuthFailureCannotOverwriteRotatedCredential(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	stub.mu.Lock()
	stub.cancelResponse = func(_ context.Context, token string, _ remoteapi.CancelRequest) error {
		if token == "rotated-device-secret" {
			return nil
		}
		close(entered)
		<-finish
		return &remoteapi.ResponseError{StatusCode: 401, Code: "device_credential_invalid"}
	}
	stub.mu.Unlock()
	post(t, server, "/api/tunnel/stop", "{}")
	<-entered
	a.mu.Lock()
	a.loginState = "rotation"
	a.mu.Unlock()
	c := identity()
	c.DeviceAccessToken = "rotated-device-secret"
	if err := a.installIdentity("rotation", c); err != nil {
		t.Fatal(err)
	}
	close(finish)
	eventually(t, func() bool { return taskCount(a) == 0 })
	if stub.count() != 1 {
		t.Fatal("late auth failure restarted acquisition")
	}
}

func TestExplicitCleanupRetryRecoversAfterServerUpgrade(t *testing.T) {
	a, _, stub, server := uncertainFixture(t)
	var available atomic.Bool
	stub.mu.Lock()
	stub.cancelResponse = func(context.Context, string, remoteapi.CancelRequest) error {
		if available.Load() {
			return nil
		}
		return &remoteapi.ResponseError{StatusCode: 404}
	}
	stub.mu.Unlock()
	post(t, server, "/api/tunnel/stop", "{}")
	eventually(t, func() bool { return fetchStatus(t, server)["lastError"] == "cancel_not_supported" })
	available.Store(true)
	post(t, server, "/api/cancellation/retry", "{}")
	eventually(t, func() bool { return taskCount(a) == 0 })
	if stub.count() != 1 || fetchStatus(t, server)["desired"] != false {
		t.Fatal("manual cleanup retry started access")
	}
}

func TestCLIOriginalGrantCannotBypassCancellationBarrier(t *testing.T) {
	a, _, _, _ := fixture(t)
	cfg := identity()
	task := &cancelTask{UserID: cfg.Device.UserID, DeviceID: cfg.Device.ID, RequestID: "unresolved", AccessToken: cfg.DeviceAccessToken}
	if err := remote.SavePrivateJSON(a.journalPath(), cancellationJournal{Version: 1, Tasks: []*cancelTask{task}}); err != nil {
		t.Fatal(err)
	}
	if err := ensureCLIGrantSettled(a.configPath, cfg); err == nil || err.Error() != "cancel_pending" {
		t.Fatal("CLI bypassed original identity cleanup")
	}
	cfg.Device.UserID = "other-account"
	if err := ensureCLIGrantSettled(a.configPath, cfg); err != nil {
		t.Fatal("CLI mixed cancellation accounts")
	}
	if err := os.WriteFile(a.journalPath(), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureCLIGrantSettled(a.configPath, cfg); err == nil || err.Error() != "cancel_storage_failed" {
		t.Fatal("CLI ignored damaged private journal")
	}
}

func TestInterruptedPendingIDSurvivesFailedRecoveryJournalWrite(t *testing.T) {
	a, _, _, _ := fixture(t)
	a.mu.Lock()
	a.config.Desired = true
	a.config.PendingRequestID = "interrupted-v4-request"
	cfg := cloneConfig(*a.config)
	a.saveJournal = func(string, any) error { return errors.New("disk unavailable") }
	a.mu.Unlock()
	if err := remote.SaveConfig(a.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	a.loadCancellations()
	a.closeCancellations()
	saved, err := remote.LoadConfig(a.configPath)
	if err != nil || saved.PendingRequestID != cfg.PendingRequestID {
		t.Fatal("failed recovery record erased the only durable original request ID")
	}
	restored, _, server := newTestApp(t, saved, a.penguinGoURL)
	restored.configPath = a.configPath
	fresh := &slotStub{}
	restored.newSlotClient = func(remote.Config) (slotClient, error) { return fresh, nil }
	restored.loadCancellations()
	restored.restoreIntent()
	eventually(t, func() bool { return taskCount(restored) == 0 && cancelCount(fresh) == 1 })
	fresh.mu.Lock()
	defer fresh.mu.Unlock()
	if fresh.cancels[0].RequestID != cfg.PendingRequestID || len(fresh.requests) != 0 || fetchStatus(t, server)["desired"] != false {
		t.Fatal("restart lost cancellation identity or replayed acquisition")
	}
}
