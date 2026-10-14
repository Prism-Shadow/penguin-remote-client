package frpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	frplog "github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/xlog"
	goliblog "github.com/fatedier/golib/log"
)

func newTestDiagnosticWriter(t *testing.T, out io.Writer) (*diagnosticWriter, func()) {
	t.Helper()
	w := newDiagnosticWriter(out)
	var once sync.Once
	finish := func() {
		t.Helper()
		once.Do(func() { close(w.events) })
		select {
		case <-w.done:
		case <-time.After(time.Second):
			t.Fatal("diagnostic worker did not finish")
		}
	}
	t.Cleanup(finish)
	return w, finish
}

func TestDiagnosticWriterOnlyEmitsSafeEvents(t *testing.T) {
	var output bytes.Buffer
	w, finish := newTestDiagnosticWriter(t, &output)
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	logger := goliblog.New(goliblog.WithOutput(w), goliblog.WithLevel(goliblog.TraceLevel))
	// Include secrets in caller prefixes, recognized error messages, and an
	// entirely unknown log entry. None may reach the diagnostic output.
	for _, message := range []string{
		"send heartbeat to server", "heartbeat write started",
		"heartbeat write completed duration_ms=12", "receive heartbeat from server",
		"heartbeat timeout", "control read failed: mux_keepalive_timeout",
		"yamux: keepalive failed: i/o deadline reached secret-token",
		"pong message contains error: credential=secret-token leaseId=secret-lease",
		"unknown raw request Cookie=secret-cookie session=secret-session",
		"heartbeat write completed duration_ms=secret-token",
	} {
		line := fmt.Sprintf("2026-10-12 08:00:00.000 [D] [client/control.go:10] [secret-device] [secret-grant] %s\n", message)
		_, _ = w.WriteLog([]byte(line), goliblog.DebugLevel, now)
	}
	logger.Debug("server data should also be dropped")
	finish()
	if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "Cookie") || strings.Contains(output.String(), "credential") {
		t.Fatal("raw private log data escaped the allowlist")
	}
	var events []diagnosticEvent
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var event diagnosticEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Time != now.Format(time.RFC3339Nano) {
			t.Fatal("diagnostic timestamp was not retained")
		}
		events = append(events, event)
	}
	if len(events) != 8 || events[2].DurationMs == nil || *events[2].DurationMs != 12 || events[6].Reason != "timeout" {
		t.Fatal("heartbeat or mux diagnostics missing")
	}
}

func TestDiagnosticWriterHandlesConcurrentWritesAndDropsOtherSources(t *testing.T) {
	var output bytes.Buffer
	w, finish := newTestDiagnosticWriter(t, &output)
	_, _ = w.Write([]byte("2026-10-12 08:00:00.000 [W] [server/control.go:10] heartbeat timeout\n"))
	var group sync.WaitGroup
	for range 50 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _ = w.Write([]byte("2026-10-12 08:00:00.000 [T] [client/connector.go:10] [run] [ERR] yamux: keepalive failed: connection write timeout\n"))
		}()
	}
	group.Wait()
	finish()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 50 {
		t.Fatal("diagnostic lines were lost or server logs mixed into client logs")
	}
	for _, line := range lines {
		var event diagnosticEvent
		if json.Unmarshal([]byte(line), &event) != nil || event.Event != "mux_keepalive_failed" || event.Reason != "mux_write_timeout" {
			t.Fatal("concurrent diagnostic output was corrupted")
		}
	}
}

type failedDiagnosticOutput struct{}

func (failedDiagnosticOutput) Write([]byte) (int, error) { return 0, fmt.Errorf("disk unavailable") }

func TestDiagnosticOutputFailureDoesNotAffectTunnelLogging(t *testing.T) {
	w, finish := newTestDiagnosticWriter(t, failedDiagnosticOutput{})
	line := []byte("2026-10-12 08:00:00.000 [D] [client/control.go:10] heartbeat timeout\n")
	if n, err := w.Write(line); n != len(line) || err != nil {
		t.Fatal("logging failure reached transport")
	}
	finish()
}

type blockedDiagnosticOutput struct {
	bytes.Buffer
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (out *blockedDiagnosticOutput) Write(p []byte) (int, error) {
	out.once.Do(func() { close(out.started) })
	<-out.release
	return out.Buffer.Write(p)
}

func TestDiagnosticOutputBackpressureCannotBlockProtocol(t *testing.T) {
	out := &blockedDiagnosticOutput{started: make(chan struct{}), release: make(chan struct{})}
	w, finish := newTestDiagnosticWriter(t, out)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(out.release) }) }
	t.Cleanup(release)
	line := []byte("2026-10-12 08:00:00.000 [D] [client/control.go:10] heartbeat timeout\n")
	_, _ = w.Write(line)
	select {
	case <-out.started:
	case <-time.After(time.Second):
		t.Fatal("diagnostic output did not start")
	}
	previous := activeDiagnostics
	activeDiagnostics = w
	defer func() { activeDiagnostics = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &Runner{denial: make(chan *ProtocolDenial, 1), cancel: cancel}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range diagnosticQueueCapacity + 32 {
			_, _ = w.Write(line)
		}
		runner.protocolError("Ping", "penguin_remote:grant_revoked:stop")
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("blocked diagnostic output stalled protocol handling")
	}
	if ctx.Err() == nil || w.dropped.Load() != 33 || len(w.events) != diagnosticQueueCapacity {
		t.Fatal("backpressure did not preserve cancellation and bounded queue")
	}
	release()
	finish()
	var dropped uint64
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var event diagnosticEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event == "diagnostic_events_dropped" {
			dropped += event.DroppedEvents
		}
	}
	if dropped != 33 {
		t.Fatal("dropped diagnostics were not reported after output resumed")
	}
}

func TestDiagnosticMuxEventsUseRealTraceWriterCaller(t *testing.T) {
	var output bytes.Buffer
	w, finish := newTestDiagnosticWriter(t, &output)
	previous := frplog.Logger
	frplog.Logger = frplog.Logger.WithOptions(goliblog.WithOutput(w), goliblog.WithLevel(goliblog.TraceLevel))
	defer func() { frplog.Logger = previous }()
	xl := xlog.New()
	xl.AppendPrefix("secret-grant")
	writer := xlog.NewTraceWriter(xl)
	for _, message := range []string{
		"[ERR] yamux: keepalive failed: keepalive timeout secret-token",
		"[ERR] yamux: Failed to write header: connection write timeout secret-token",
		"[ERR] yamux: Failed to read header: EOF",
		"unknown raw message cookie=secret-cookie",
		"heartbeat timeout",
	} {
		_, _ = writer.Write([]byte(message))
	}
	finish()
	if strings.Contains(output.String(), "secret") {
		t.Fatal("yamux diagnostics exposed private log data")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	expected := []struct{ event, reason string }{
		{"mux_keepalive_failed", "mux_keepalive_timeout"},
		{"mux_write_failed", "mux_write_timeout"},
		{"mux_read_failed", "eof"},
	}
	if len(lines) != len(expected) {
		t.Fatalf("real yamux log events missing or unrecognized messages retained: %s", output.String())
	}
	for i, line := range lines {
		var event diagnosticEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != expected[i].event || event.Reason != expected[i].reason {
			t.Fatalf("unexpected yamux event: %+v", event)
		}
	}
}
