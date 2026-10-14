// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package diagnostics

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func finish(t *testing.T, r *Recorder) {
	t.Helper()
	close(r.events)
	select {
	case <-r.done:
	case <-time.After(time.Second):
		t.Fatal("diagnostic output did not drain")
	}
}

func TestSafeRecorderAndMuxWriterDropPrivateData(t *testing.T) {
	var out bytes.Buffer
	r := New("frps", &out)
	r.Record(Event{Event: "transport_open", Reason: "secret-token"})
	r.Record(Event{Event: "secret-token"})
	r.Record(Event{Event: "transport_open", Operation: "secret-cookie"})
	w := MuxWriter(WithConnection(context.Background(), 3), r, io.Discard)
	_, _ = w.Write([]byte("[ERR] yamux: keepalive failed: keepalive timeout secret-token grant-secret\n"))
	_, _ = w.Write([]byte("Cookie=secret-cookie arbitrary payload"))
	finish(t, r)
	if strings.Contains(out.String(), "secret") {
		t.Fatal("private data leaked")
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "mux_keepalive_failed" || event.Reason != "mux_keepalive_timeout" || event.Connection != 3 {
		t.Fatalf("wrong mux diagnostic: %+v", event)
	}
}

type blockingOutput struct {
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (w *blockingOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

func TestRecorderIsBoundedWhenOutputBlocks(t *testing.T) {
	w := &blockingOutput{release: make(chan struct{}), entered: make(chan struct{})}
	r := New("frps", w)
	r.Record(Event{Event: "transport_open"})
	<-w.entered
	completed := make(chan struct{})
	go func() {
		for range queueCapacity + 10 {
			r.Record(Event{Event: "transport_open"})
		}
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(time.Second):
		close(w.release)
		t.Fatal("protocol could be blocked by diagnostics")
	}
	if len(r.events) != queueCapacity || r.dropped.Load() != 10 {
		t.Fatal("output queue is not bounded")
	}
	close(w.release)
	finish(t, r)
}

func TestTracedConnectionPreservesPayloadAndErrors(t *testing.T) {
	var out bytes.Buffer
	r := New("frpc", &out)
	a, b := net.Pipe()
	ctx, wrapped := WrapConn(context.Background(), a, r)
	payload := bytes.Repeat([]byte("synthetic-session-data"), 300000)
	go func() { _, _ = b.Write(payload); _ = b.Close() }()
	actual, err := io.ReadAll(wrapped)
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("diagnostics altered stream payload")
	}
	_ = wrapped.Close()
	finish(t, r)
	if ConnectionID(ctx) == 0 || strings.Contains(out.String(), "synthetic-session-data") {
		t.Fatal("invalid connection ID or leaked payload")
	}
	var failure, closed bool
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var event Event
		if json.Unmarshal([]byte(line), &event) != nil {
			t.Fatal("invalid event")
		}
		if event.Event == "transport_read_failed" {
			failure = event.Reason == "eof" && event.BytesRead == uint64(len(payload))
		}
		if event.Event == "transport_closed" {
			closed = event.Reason == "local_close"
		}
	}
	if !failure || !closed {
		t.Fatal("missing terminal cause and transfer counts")
	}
}

func TestDiagnosticsOffLeavesConnectionUnchanged(t *testing.T) {
	t.Setenv("PENGUIN_REMOTE_DIAGNOSTICS", "0")
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx := context.Background()
	nextCtx, nextConn := WrapConn(ctx, a, For("frps"))
	if nextCtx != ctx || nextConn != a {
		t.Fatal("disabled diagnostics changed transport")
	}
}

func TestDiagnosticErrorClassesNeverExposeRawDetails(t *testing.T) {
	for _, test := range []struct {
		err      error
		expected string
	}{
		{&net.DNSError{Err: "secret-host lookup failed", Name: "secret-host"}, "dns_failed"},
		{x509.UnknownAuthorityError{}, "tls_failed"},
		{syscall.ECONNREFUSED, "connection_refused"},
		{context.DeadlineExceeded, "timeout"},
		{io.ErrUnexpectedEOF, "unexpected_eof"},
	} {
		if actual := Reason(test.err); actual != test.expected {
			t.Fatalf("unexpected error class: %s", actual)
		}
	}
}
