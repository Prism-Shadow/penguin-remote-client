package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Prism-Shadow/penguin-remote-client/internal/frpclient"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remote"
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRuntime struct {
	closed    atomic.Bool
	connected atomic.Bool
	exit      chan error
}

func (f *fakeRuntime) Run(ctx context.Context) error {
	select {
	case err := <-f.exit:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *fakeRuntime) Close()               { f.closed.Store(true) }
func (f *fakeRuntime) DataRoot() string     { return "test-root" }
func (f *fakeRuntime) ProxyConnected() bool { return f.connected.Load() }

type fakeRuntimeFactory struct {
	mu  sync.Mutex
	all []*fakeRuntime
}

func (f *fakeRuntimeFactory) build(ctx context.Context, c remote.Config) (tunnelRuntime, error) {
	r := &fakeRuntime{exit: make(chan error, 1)}
	f.mu.Lock()
	f.all = append(f.all, r)
	f.mu.Unlock()
	return r, nil
}
func (f *fakeRuntimeFactory) latest() *fakeRuntime {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.all) == 0 {
		return nil
	}
	return f.all[len(f.all)-1]
}

type slotStub struct {
	mu              sync.Mutex
	requests        []remoteapi.AcquireRequest
	recoveries      []remoteapi.RecoverRequest
	release         []remoteapi.ReleaseRequest
	respond         func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error)
	recoverResponse func(context.Context, remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error)
	status          *remoteapi.StatusResponse
	statusErr       error
	cancels         []remoteapi.CancelRequest
	cancelResponse  func(context.Context, string, remoteapi.CancelRequest) error
}

func (f *slotStub) Acquire(ctx context.Context, _ string, r remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	respond := f.respond
	f.mu.Unlock()
	if respond != nil {
		return respond(ctx, r)
	}
	return acquired(), nil
}
func (f *slotStub) Recover(ctx context.Context, _ string, r remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
	f.mu.Lock()
	f.recoveries = append(f.recoveries, r)
	respond := f.recoverResponse
	f.mu.Unlock()
	if respond != nil {
		return respond(ctx, r)
	}
	return nil, &remoteapi.ResponseError{StatusCode: http.StatusNotFound}
}
func (f *slotStub) Release(_ context.Context, _ string, r remoteapi.ReleaseRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.release = append(f.release, r)
	return errors.New("offline")
}
func (f *slotStub) Cancel(ctx context.Context, token string, r remoteapi.CancelRequest) error {
	f.mu.Lock()
	f.cancels = append(f.cancels, r)
	respond := f.cancelResponse
	f.mu.Unlock()
	if respond != nil {
		return respond(ctx, token, r)
	}
	return nil
}
func (f *slotStub) Status(context.Context, string) (*remoteapi.StatusResponse, error) {
	return f.status, f.statusErr
}
func (f *slotStub) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.requests) }
func acquired() *remoteapi.AcquireResponse {
	expiry := time.Now().Add(time.Hour).Format(time.RFC3339)
	return &remoteapi.AcquireResponse{ProtocolVersion: 2, Entitlement: remoteapi.RemoteEntitlement{Status: "active", ExpiresAt: &expiry}, Grant: remoteapi.SlotGrant{GrantID: "grant", Credential: "tunnel-secret", SlotIndex: 1, Generation: 7}, Tunnel: remoteapi.SlotTunnel{ProxyName: "device.slot", CustomDomain: "u.remote.test", TransportToken: "transport-secret", PublicURL: "https://u.remote.test", ServerAddr: "localhost", ServerPort: 7000}}
}
func identity() remote.Config {
	return remote.Config{PenguinGoURL: remoteapi.DefaultBaseURL, Version: 4, Source: remote.SourceLogin, Device: remote.DeviceConfig{ID: "device", UserID: "1"}, DeviceAccessToken: "device-secret", Harness: remote.HarnessConfig{LocalURL: remote.DefaultHarnessURL}}
}
func newTestApp(t *testing.T, cfg *remote.Config, url string) (*appServer, *fakeRuntimeFactory, *httptest.Server) {
	t.Helper()
	f := &fakeRuntimeFactory{}
	a := &appServer{configPath: filepath.Join(t.TempDir(), "remote.json"), penguinGoURL: url, runnerStatus: "stopped", language: "zh", theme: "system", localURL: remote.DefaultHarnessURL, newRuntime: f.build}
	if cfg != nil {
		c := cloneConfig(*cfg)
		c.PenguinGoURL = apiIdentity(url)
		a.config = &c
		_ = remote.SaveConfig(a.configPath, c)
	}
	server := httptest.NewServer(a.routes())
	t.Cleanup(func() {
		a.closeCancellations()
		a.mu.Lock()
		rt := a.detachLocked()
		a.mu.Unlock()
		closeRunning(rt)
		server.Close()
	})
	return a, f, server
}
func fetchStatus(t *testing.T, s *httptest.Server) map[string]any {
	t.Helper()
	r, err := http.Get(s.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var v map[string]any
	_ = json.NewDecoder(r.Body).Decode(&v)
	return v
}
func post(t *testing.T, s *httptest.Server, path, body string) {
	t.Helper()
	r, e := http.Post(s.URL+path, "application/json", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("POST %s: %d", path, r.StatusCode)
	}
}
func eventually(t *testing.T, check func() bool) {
	t.Helper()
	until := time.Now().Add(6 * time.Second)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func fixture(t *testing.T) (*appServer, *fakeRuntimeFactory, *slotStub, *httptest.Server) {
	c := identity()
	a, f, s := newTestApp(t, &c, "")
	stub := &slotStub{}
	a.newSlotClient = func(remote.Config) (slotClient, error) { return stub, nil }
	return a, f, stub, s
}
func TestStartRetryStableRequestDuplicateClicksAndStop(t *testing.T) {
	_, factory, stub, s := fixture(t)
	var calls atomic.Int32
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("network timeout")
		}
		return acquired(), nil
	}
	post(t, s, "/api/tunnel/start", "{}")
	post(t, s, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	stub.mu.Lock()
	if len(stub.requests) != 2 || stub.requests[0].RequestID != stub.requests[1].RequestID {
		t.Fatal("start retry changed request or duplicated click")
	}
	stub.mu.Unlock()
	post(t, s, "/api/tunnel/stop", "{}")
	eventually(t, func() bool { return factory.latest().closed.Load() })
	if fetchStatus(t, s)["desired"] != false {
		t.Fatal("stop retained intent")
	}
	time.Sleep(100 * time.Millisecond)
	if stub.count() != 2 {
		t.Fatal("stop triggered acquire")
	}
}
func TestLateAcquireAfterStopReleasesExactGrantWithoutRuntime(t *testing.T) {
	_, f, stub, s := fixture(t)
	entered := make(chan struct{})
	proceed := make(chan struct{})
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		close(entered)
		<-proceed
		return acquired(), nil
	}
	post(t, s, "/api/tunnel/start", "{}")
	<-entered
	post(t, s, "/api/tunnel/stop", "{}")
	close(proceed)
	eventually(t, func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return len(stub.release) > 0 })
	if f.latest() != nil {
		t.Fatal("hidden runtime after stop")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, r := range stub.release {
		if r.GrantID != "grant" || r.Generation != 7 {
			t.Fatal("released wrong generation")
		}
	}
}
func TestRuntimeReconnectNeverAcquires(t *testing.T) {
	_, f, stub, s := fixture(t)
	post(t, s, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return f.latest() != nil })
	first := f.latest()
	first.connected.Store(true)
	if fetchStatus(t, s)["connectionStatus"] != "connected" {
		t.Fatal("proxy state ignored")
	}
	first.connected.Store(false)
	if fetchStatus(t, s)["connectionStatus"] != "reconnecting" {
		t.Fatal("real reconnect status missing")
	}
	first.exit <- errors.New("frps restart")
	eventually(t, func() bool { return f.latest() != nil && f.latest() != first })
	if stub.count() != 1 {
		t.Fatal("reconnect acquired slot")
	}
}
func TestExplicitTakeoverStopsAutomaticReconnect(t *testing.T) {
	_, f, stub, s := fixture(t)
	post(t, s, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return f.latest() != nil })
	f.latest().exit <- &frpclient.ProtocolDenial{Code: "grant_superseded"}
	eventually(t, func() bool { return fetchStatus(t, s)["runnerStatus"] == "grant_superseded" })
	if fetchStatus(t, s)["desired"] != false {
		t.Fatal("takeover intent still on")
	}
	post(t, s, "/api/wake", "{}")
	time.Sleep(150 * time.Millisecond)
	if stub.count() != 1 {
		t.Fatal("wake reacquired")
	}
	post(t, s, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return stub.count() == 2 })
}

func TestLegacyOrOtherSavedGrantDenialRequiresManualReconnect(t *testing.T) {
	for _, reason := range []string{"grant_superseded", "grant_revoked", "grant_credential_invalid"} {
		t.Run(reason, func(t *testing.T) {
			cfg := identity()
			if err := cfg.ApplyAcquire(acquired()); err != nil {
				t.Fatal(err)
			}
			cfg.Desired = true
			a, factory, server := newTestApp(t, &cfg, "")
			stub := &slotStub{respond: func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
				out := acquired()
				out.Grant.GrantID = "new-grant"
				out.Grant.Generation++
				return out, nil
			}}
			a.newSlotClient = func(remote.Config) (slotClient, error) { return stub, nil }
			a.restoreIntent()
			eventually(t, func() bool { return factory.latest() != nil })
			first := factory.latest()
			first.exit <- &frpclient.ProtocolDenial{Operation: "Login", Code: reason, InitialLogin: true}
			eventually(t, func() bool {
				status := fetchStatus(t, server)
				return status["desired"] == false && status["connectionStatus"] == "idle" &&
					status["runnerStatus"] == reason && status["lastError"] == reason
			})
			saved, err := remote.LoadConfig(a.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !first.closed.Load() || saved.Desired || saved.Grant != nil || saved.Tunnel != nil || saved.PendingRequestID != "" {
				t.Fatal("denied connection was not closed and cleared from disk")
			}
			a.restoreIntent()
			post(t, server, "/api/wake", "{}")
			post(t, server, "/api/subscription/refresh", "{}")
			if factory.latest() != first || stub.count() != 0 || fetchStatus(t, server)["desired"] != false {
				t.Fatal("restoring, waking or refreshing restarted a denied grant")
			}
			post(t, server, "/api/tunnel/start", "{}")
			eventually(t, func() bool { return factory.latest() != first })
			factory.latest().connected.Store(true)
			eventually(t, func() bool { return fetchStatus(t, server)["connectionStatus"] == "connected" })
			status := fetchStatus(t, server)
			if stub.count() != 1 || status["connectionStatus"] != "connected" || status["lastError"] != "" {
				t.Fatalf("manual reconnect did not clear the denial and connect: %+v", status)
			}
			saved, err = remote.LoadConfig(a.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !saved.Desired || saved.Grant == nil || saved.Grant.GrantID != "new-grant" || saved.Grant.Generation != 8 ||
				saved.Harness.LocalURL != cfg.Harness.LocalURL {
				t.Fatal("manual reconnect did not persist the new grant for the same Harness")
			}
		})
	}
}

func restoreTestGrant(t *testing.T, stub *slotStub) (*appServer, *fakeRuntimeFactory, *httptest.Server, *fakeRuntime) {
	t.Helper()
	cfg := identity()
	if err := cfg.ApplyAcquire(acquired()); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.TrustedCAFile = "saved-ca.pem"
	cfg.Desired = true
	a, factory, server := newTestApp(t, &cfg, "")
	a.newSlotClient = func(remote.Config) (slotClient, error) { return stub, nil }
	a.restoreIntent()
	eventually(t, func() bool { return factory.latest() != nil })
	return a, factory, server, factory.latest()
}

func supersededLogin(initial bool) *frpclient.ProtocolDenial {
	retry := false
	return &frpclient.ProtocolDenial{Operation: "Login", Code: "grant_superseded", InitialLogin: initial, RetryAllowed: &retry}
}

func recoveredGrant() *remoteapi.AcquireResponse {
	out := acquired()
	out.Grant.GrantID = "recovered-grant"
	out.Grant.Generation++
	return out
}

func TestRestoreRecoversSameSlotAndRetriesWithOriginalRequest(t *testing.T) {
	calls := make(chan remoteapi.RecoverRequest, 2)
	var firstAttempt time.Time
	stub := &slotStub{recoverResponse: func(_ context.Context, req remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
		if firstAttempt.IsZero() {
			firstAttempt = time.Now()
			calls <- req
			return nil, &remoteapi.ResponseError{StatusCode: 429, Code: "remote_rate_limited", RetryAfter: 1500 * time.Millisecond}
		}
		calls <- req
		if time.Since(firstAttempt) < 1500*time.Millisecond {
			t.Error("wake bypassed Retry-After")
		}
		return recoveredGrant(), nil
	}}
	a, factory, server, first := restoreTestGrant(t, stub)
	first.exit <- supersededLogin(true)
	var original remoteapi.RecoverRequest
	select {
	case original = <-calls:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not start")
	}
	if original.RequestID == "" || original.GrantID != "grant" || original.SlotIndex != 1 || original.Generation != 7 ||
		original.HarnessLocalURL != remote.DefaultHarnessURL {
		t.Fatalf("invalid recovery binding: %+v", original)
	}
	post(t, server, "/api/wake", "{}")
	eventually(t, func() bool { return factory.latest() != first })
	if retry := <-calls; retry != original {
		t.Fatalf("retry changed the recovery request: %+v", retry)
	}
	saved, err := remote.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Desired || saved.Grant == nil || saved.Grant.GrantID != "recovered-grant" || saved.Grant.Generation != 8 ||
		saved.Tunnel == nil || saved.Tunnel.TrustedCAFile != "saved-ca.pem" ||
		saved.Harness.LocalURL != original.HarnessLocalURL || saved.PendingRequestID != "" || stub.count() != 0 {
		t.Fatal("automatic recovery did not save the new grant or invoked manual acquire")
	}
	second := factory.latest()
	second.connected.Store(true)
	eventually(t, func() bool { return fetchStatus(t, server)["connectionStatus"] == "connected" })
	second.exit <- supersededLogin(true)
	eventually(t, func() bool { return fetchStatus(t, server)["runnerStatus"] == "grant_superseded" })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.recoveries) != 2 || len(stub.requests) != 0 {
		t.Fatal("superseding the recovered grant initiated a new recovery")
	}
}

func TestRestoreRecoveryStopsWhenSlotIsHeldOrCannotBeVerified(t *testing.T) {
	for _, tc := range []struct {
		name, reason, state, notice string
		status                      int
	}{
		{name: "online holder", reason: "slot_held_by_another_device", state: "online", notice: "slot_held_by_another_device_online", status: 409},
		{name: "holder without online evidence", reason: "slot_held_by_another_device", notice: "slot_held_by_another_device", status: 409},
		{name: "unverified", reason: "grant_ownership_unverified", notice: "grant_ownership_unverified", status: 409},
		{name: "request quota", reason: "remote_requests_quota_exhausted", notice: "remote_requests_quota_exhausted", status: 409},
		{name: "limiter capacity", reason: "remote_limiter_capacity", notice: "remote_limiter_capacity", status: 409},
		{name: "invalid limiter config", reason: "remote_budget_configuration_invalid", notice: "remote_budget_configuration_invalid", status: 409},
		{name: "request conflict", reason: "request_id_conflict", notice: "request_id_conflict", status: 409},
		{name: "Go without recovery", notice: "grant_superseded", status: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &slotStub{recoverResponse: func(context.Context, remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
				return nil, &remoteapi.ResponseError{StatusCode: tc.status, Code: tc.reason, ConnectionState: tc.state}
			}}
			a, factory, server, first := restoreTestGrant(t, stub)
			first.exit <- supersededLogin(true)
			eventually(t, func() bool {
				status := fetchStatus(t, server)
				return status["desired"] == false && status["cancelPending"] == false && status["lastError"] == tc.notice
			})
			a.restoreIntent()
			post(t, server, "/api/wake", "{}")
			if factory.latest() != first || stub.count() != 0 {
				t.Fatal("denial triggered a connection or manual acquisition")
			}
			post(t, server, "/api/tunnel/start", "{}")
			eventually(t, func() bool { return factory.latest() != first })
			factory.latest().connected.Store(true)
			eventually(t, func() bool { return fetchStatus(t, server)["connectionStatus"] == "connected" })
			stub.mu.Lock()
			defer stub.mu.Unlock()
			if len(stub.recoveries) != 1 || len(stub.requests) != 1 || len(stub.cancels) != 0 {
				t.Fatal("expected only one recovery and one explicit manual acquire")
			}
		})
	}
}

func TestRestoreRecoveryDoesNotRunAfterEstablishedLogin(t *testing.T) {
	stub := &slotStub{}
	_, _, server, first := restoreTestGrant(t, stub)
	first.exit <- supersededLogin(false)
	eventually(t, func() bool { return fetchStatus(t, server)["runnerStatus"] == "grant_superseded" })
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.recoveries) != 0 || len(stub.requests) != 0 {
		t.Fatal("takeover after an established login triggered recovery")
	}
}

func TestRecoveryRequestQuotaCancelsOnlyAfterAnAmbiguousOutcome(t *testing.T) {
	for _, tc := range []struct {
		name        string
		firstError  error
		wantCancels int
	}{
		{name: "known rate limit", firstError: &remoteapi.ResponseError{StatusCode: 429, Code: "remote_rate_limited"}},
		{name: "unknown network outcome", firstError: errors.New("lost response"), wantCancels: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			stub := &slotStub{recoverResponse: func(context.Context, remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
				attempts++
				if attempts == 1 {
					return nil, tc.firstError
				}
				return nil, &remoteapi.ResponseError{StatusCode: 409, Code: "remote_requests_quota_exhausted"}
			}}
			_, _, server, first := restoreTestGrant(t, stub)
			first.exit <- supersededLogin(true)
			eventually(t, func() bool {
				status := fetchStatus(t, server)
				return status["lastError"] == "remote_requests_quota_exhausted" && status["desired"] == false && status["cancelPending"] == false
			})
			stub.mu.Lock()
			defer stub.mu.Unlock()
			if len(stub.recoveries) != 2 || stub.recoveries[0].RequestID != stub.recoveries[1].RequestID ||
				len(stub.cancels) != tc.wantCancels || len(stub.requests) != 0 {
				t.Fatal("quota denial did not preserve the appropriate cancellation fence")
			}
		})
	}
}

func TestStopDuringKnownRecoveryRateLimitHasNoRequestToCancel(t *testing.T) {
	stub := &slotStub{recoverResponse: func(context.Context, remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
		return nil, &remoteapi.ResponseError{StatusCode: 429, Code: "remote_rate_limited", RetryAfter: time.Minute}
	}}
	a, factory, server, first := restoreTestGrant(t, stub)
	first.exit <- supersededLogin(true)
	eventually(t, func() bool {
		return fetchStatus(t, server)["lastError"] == "service_unavailable" && taskCount(a) == 0
	})
	post(t, server, "/api/tunnel/stop", "{}")
	status := fetchStatus(t, server)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if status["desired"] != false || status["cancelPending"] != false || factory.latest() != first ||
		len(stub.recoveries) != 1 || len(stub.cancels) != 0 {
		t.Fatal("stopping a definitively rate-limited recovery attempted cancellation or reconnected")
	}
}

func TestStopFencesInFlightRecovery(t *testing.T) {
	started := make(chan remoteapi.RecoverRequest, 1)
	reply := make(chan struct{})
	releaseReply := sync.OnceFunc(func() { close(reply) })
	defer releaseReply()
	stub := &slotStub{recoverResponse: func(_ context.Context, req remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
		started <- req
		<-reply
		return recoveredGrant(), nil
	}}
	a, factory, server, first := restoreTestGrant(t, stub)
	first.exit <- supersededLogin(true)
	var request remoteapi.RecoverRequest
	select {
	case request = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not start")
	}
	saved, err := remote.LoadConfig(a.configPath)
	if err != nil || saved.PendingRequestID != "" {
		t.Fatal("an interrupted recovery could be replayed as manual acquire")
	}
	post(t, server, "/api/tunnel/stop", "{}")
	eventually(t, func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return len(stub.cancels) == 1 && stub.cancels[0].RequestID == request.RequestID
	})
	releaseReply()
	eventually(t, func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		for _, release := range stub.release {
			if release.GrantID == "recovered-grant" && release.Generation == 8 {
				return true
			}
		}
		return false
	})
	if factory.latest() != first || stub.count() != 0 || fetchStatus(t, server)["desired"] != false {
		t.Fatal("stop did not fence the recovering connection")
	}
}

func TestRestartCancelsInterruptedRecovery(t *testing.T) {
	started := make(chan remoteapi.RecoverRequest, 1)
	reply := make(chan struct{})
	defer close(reply)
	stub := &slotStub{recoverResponse: func(_ context.Context, req remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
		started <- req
		<-reply
		return nil, errors.New("lost recovery response")
	}}
	a, _, _, first := restoreTestGrant(t, stub)
	first.exit <- supersededLogin(true)
	var request remoteapi.RecoverRequest
	select {
	case request = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not start")
	}
	a.closeCancellations()
	a.mu.Lock()
	rt := a.detachLocked()
	a.mu.Unlock()
	closeRunning(rt)
	saved, err := remote.LoadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	restored, factory, server := newTestApp(t, saved, a.penguinGoURL)
	restored.configPath = a.configPath
	fresh := &slotStub{}
	restored.newSlotClient = func(remote.Config) (slotClient, error) { return fresh, nil }
	restored.loadCancellations()
	restored.restoreIntent()
	eventually(t, func() bool {
		fresh.mu.Lock()
		defer fresh.mu.Unlock()
		return len(fresh.cancels) == 1 && fresh.cancels[0].RequestID == request.RequestID
	})
	post(t, server, "/api/wake", "{}")
	if factory.latest() != nil || fresh.count() != 0 || fetchStatus(t, server)["desired"] != false {
		t.Fatal("restart turned an interrupted recovery into a new connection")
	}
}

func TestRecoveryRejectsAnotherSlotOrCustomTLSAddress(t *testing.T) {
	for _, tc := range []struct {
		reason string
		change func(*remoteapi.AcquireResponse)
	}{
		{reason: "invalid_request", change: func(out *remoteapi.AcquireResponse) { out.Grant.SlotIndex = 2 }},
		{reason: "frps_tls_endpoint_changed", change: func(out *remoteapi.AcquireResponse) { out.Tunnel.ServerAddr = "other.remote.test" }},
		{reason: "frps_tls_endpoint_changed", change: func(out *remoteapi.AcquireResponse) { out.Tunnel.ServerPort++ }},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			stub := &slotStub{recoverResponse: func(context.Context, remoteapi.RecoverRequest) (*remoteapi.AcquireResponse, error) {
				out := recoveredGrant()
				tc.change(out)
				return out, nil
			}}
			_, factory, server, first := restoreTestGrant(t, stub)
			first.exit <- supersededLogin(true)
			eventually(t, func() bool {
				status := fetchStatus(t, server)
				return status["lastError"] == tc.reason && status["desired"] == false && status["cancelPending"] == false
			})
			stub.mu.Lock()
			defer stub.mu.Unlock()
			if factory.latest() != first || len(stub.requests) != 0 || len(stub.release) != 0 ||
				len(stub.recoveries) != 1 || len(stub.cancels) != 1 || stub.cancels[0].RequestID != stub.recoveries[0].RequestID {
				t.Fatal("unsafe response was used or cleanup targeted something other than the recovery request")
			}
		})
	}
}

func TestRefreshFailureAndCachedExpiryCannotStopRuntime(t *testing.T) {
	a, f, stub, s := fixture(t)
	post(t, s, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return f.latest() != nil })
	a.mu.Lock()
	a.config.Entitlement.ExpiresAt = "2000-01-01T00:00:00Z"
	a.mu.Unlock()
	stub.statusErr = errors.New("DNS failed")
	post(t, s, "/api/subscription/refresh", "{}")
	if f.latest().closed.Load() || fetchStatus(t, s)["entitlementStatus"] != "unknown" {
		t.Fatal("refresh/cache stopped tunnel")
	}
	status := fetchStatus(t, s)
	data, _ := json.Marshal(status)
	for _, secret := range []string{"device-secret", "tunnel-secret", "transport-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("status exposed secret")
		}
	}
}
func TestIdentityRefreshKeepsGrantAccountChangeCancels(t *testing.T) {
	a, f, _, s := fixture(t)
	post(t, s, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return f.latest() != nil })
	a.mu.Lock()
	a.loginState = "refresh"
	a.mu.Unlock()
	c := identity()
	c.DeviceAccessToken = "new-device-secret"
	if a.installIdentity("refresh", c) != nil {
		t.Fatal("refresh failed")
	}
	a.mu.Lock()
	if a.config.Grant == nil || a.intent == nil || a.config.DeviceAccessToken != "new-device-secret" {
		t.Fatal("same-device login lost grant")
	}
	a.loginState = "other"
	a.mu.Unlock()
	c.Device.ID = "other"
	c.Device.UserID = "2"
	if a.installIdentity("other", c) != nil {
		t.Fatal("account switch failed")
	}
	if !f.latest().closed.Load() || fetchStatus(t, s)["desired"] != false {
		t.Fatal("old account runtime survived")
	}
}

func TestSubscriptionRefreshResumesOriginalGrantWithoutLoginOrAcquire(t *testing.T) {
	_, factory, stub, server := fixture(t)
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	first := factory.latest()
	first.exit <- &frpclient.ProtocolDenial{Code: "subscription_expired"}
	eventually(t, func() bool { return fetchStatus(t, server)["runnerStatus"] == "subscription_expired" })
	if fetchStatus(t, server)["desired"] != true {
		t.Fatal("subscription denial lost user intent")
	}
	quota := 1
	stub.status = &remoteapi.StatusResponse{ProtocolVersion: 2, Device: remoteapi.Device{DeviceID: "device"}, Entitlement: acquired().Entitlement}
	stub.status.Entitlement.MaxConcurrentTunnels = &quota
	post(t, server, "/api/subscription/refresh", "{}")
	eventually(t, func() bool { return factory.latest() != first })
	if stub.count() != 1 {
		t.Fatal("subscription renewal acquired a new grant")
	}
}

func TestQuotaRefreshWaitsForTheOriginalSlotToBecomeEligible(t *testing.T) {
	_, factory, stub, server := fixture(t)
	quota := 2
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		out := acquired()
		out.Grant.SlotIndex = 2
		out.Entitlement.MaxConcurrentTunnels = &quota
		return out, nil
	}
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	first := factory.latest()
	first.exit <- &frpclient.ProtocolDenial{Code: "slot_over_quota"}
	eventually(t, func() bool { return fetchStatus(t, server)["runnerStatus"] == "slot_over_quota" })
	quota = 1
	stub.status = &remoteapi.StatusResponse{ProtocolVersion: 2, Device: remoteapi.Device{DeviceID: "device"}, Entitlement: acquired().Entitlement}
	stub.status.Entitlement.MaxConcurrentTunnels = &quota
	post(t, server, "/api/subscription/refresh", "{}")
	time.Sleep(100 * time.Millisecond)
	if factory.latest() != first {
		t.Fatal("unchanged quota triggered reconnect")
	}
	quota = 2
	post(t, server, "/api/subscription/refresh", "{}")
	eventually(t, func() bool { return factory.latest() != first })
	if stub.count() != 1 {
		t.Fatal("quota recovery acquired another slot")
	}
}

func TestLateRuntimeConstructionCannotSurviveStopOrAccountReplacement(t *testing.T) {
	for _, action := range []string{"stop", "replace-account"} {
		t.Run(action, func(t *testing.T) {
			a, _, stub, server := fixture(t)
			entered := make(chan struct{})
			finish := make(chan struct{})
			built := make(chan *fakeRuntime, 1)
			a.newRuntime = func(context.Context, remote.Config) (tunnelRuntime, error) {
				close(entered)
				<-finish
				rt := &fakeRuntime{exit: make(chan error, 1)}
				built <- rt
				return rt, nil
			}
			post(t, server, "/api/tunnel/start", "{}")
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("runtime not constructed")
			}
			if action == "stop" {
				post(t, server, "/api/tunnel/stop", "{}")
			} else {
				a.mu.Lock()
				a.loginState = "replacement"
				a.mu.Unlock()
				c := identity()
				c.Device.ID = "replacement-device"
				c.Device.UserID = "2"
				if err := a.installIdentity("replacement", c); err != nil {
					t.Fatal(err)
				}
			}
			close(finish)
			rt := <-built
			eventually(t, rt.closed.Load)
			if fetchStatus(t, server)["desired"] != false || stub.count() != 1 {
				t.Fatal("late runtime restored stopped intent")
			}
		})
	}
}

func TestLateAcquireDoesNotOverwriteOrReleaseLaterConnection(t *testing.T) {
	a, factory, stub, server := fixture(t)
	firstEntered := make(chan struct{})
	finishFirst := make(chan struct{})
	var calls atomic.Int32
	stub.respond = func(context.Context, remoteapi.AcquireRequest) (*remoteapi.AcquireResponse, error) {
		out := acquired()
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-finishFirst
			out.Grant.GrantID = "old-grant"
			out.Grant.Generation = 7
		} else {
			out.Grant.GrantID = "new-grant"
			out.Grant.Generation = 8
		}
		return out, nil
	}
	post(t, server, "/api/tunnel/start", "{}")
	<-firstEntered
	post(t, server, "/api/tunnel/stop", "{}")
	post(t, server, "/api/tunnel/start", "{}")
	eventually(t, func() bool { return factory.latest() != nil })
	close(finishFirst)
	eventually(t, func() bool { stub.mu.Lock(); defer stub.mu.Unlock(); return len(stub.release) > 0 })
	a.mu.Lock()
	grant := *a.config.Grant
	desired := a.config.Desired
	a.mu.Unlock()
	if grant.GrantID != "new-grant" || !desired || factory.latest().closed.Load() {
		t.Fatal("late response overwrote new connection")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, release := range stub.release {
		if release.GrantID != "old-grant" || release.Generation != 7 {
			t.Fatal("late release targeted new connection")
		}
	}
}

func TestCallbackNeverExposesRawErrorsAndConsumesSuccessOnce(t *testing.T) {
	callbacks := make(chan loginCallback, 2)
	var exchanges atomic.Int32
	callback := newCallbackServer("test-state", callbacks, func(context.Context, string) loginCallback {
		exchanges.Add(1)
		return loginCallback{Err: errors.New("private-device-secret raw network stack")}
	})
	server := httptest.NewServer(callback.Handler)
	defer server.Close()
	for _, query := range []string{"state=test-state&error=private-device-secret", "state=test-state&code=one-time-code", "state=test-state&code=other-code"} {
		resp, err := http.Get(server.URL + "/callback?" + query)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(data), "private-device-secret") || strings.Contains(string(data), "raw network stack") {
			t.Fatal("callback exposed raw error")
		}
	}
	if exchanges.Load() != 1 {
		t.Fatal("callback repeated code exchange")
	}
	for len(callbacks) > 0 {
		if strings.Contains((<-callbacks).Err.Error(), "private-device-secret") {
			t.Fatal("CLI callback error exposed a secret")
		}
	}
}

func TestClientDoesNotOfferIndependentProxyPreferences(t *testing.T) {
	_, _, _, server := fixture(t)
	status := fetchStatus(t, server)
	if _, ok := status["networkMode"]; ok {
		t.Fatal("network preference exposed in status")
	}
	if _, ok := status["networkProxyUrl"]; ok {
		t.Fatal("manual proxy exposed in status")
	}
	resp, err := http.Post(server.URL+"/api/network", "application/json", strings.NewReader(`{"mode":"direct","proxyUrl":"http://user:private-secret@proxy.test:8080"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("removed network settings API remains accessible")
	}
	for _, id := range []string{`id="networkMode"`, `id="networkProxy"`, `id="saveNetwork"`} {
		if strings.Contains(appPageHTML, id) {
			t.Fatal("removed network setting remains in UI")
		}
	}
}

func TestRestoreIntentUsesSavedGrantOrTheOriginalPendingRequest(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending_%t", pending), func(t *testing.T) {
			cfg := identity()
			cfg.Desired = true
			if pending {
				cfg.PendingRequestID = "original-start-request"
			} else if err := cfg.ApplyAcquire(acquired()); err != nil {
				t.Fatal(err)
			}
			a, factory, server := newTestApp(t, &cfg, "")
			stub := &slotStub{statusErr: errors.New("service_unavailable")}
			a.newSlotClient = func(remote.Config) (slotClient, error) { return stub, nil }
			a.restoreIntent()
			eventually(t, func() bool { return factory.latest() != nil })
			stub.mu.Lock()
			if pending {
				if len(stub.requests) != 1 || stub.requests[0].RequestID != "original-start-request" {
					t.Error("restart generated a new acquire request")
				}
			} else if len(stub.requests) != 0 {
				t.Error("restart acquired instead of reconnecting original grant")
			}
			stub.mu.Unlock()
			post(t, server, "/api/tunnel/stop", "{}")
			post(t, server, "/api/wake", "{}")
			post(t, server, "/api/subscription/refresh", "{}")
			if fetchStatus(t, server)["desired"] != false {
				t.Fatal("wake or status restored a stopped intent")
			}
		})
	}
}
