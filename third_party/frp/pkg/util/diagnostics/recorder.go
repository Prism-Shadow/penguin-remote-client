// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package diagnostics

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const queueCapacity = 256

// IDs are process-local counters, never frp run IDs or business credentials.
type Event struct {
	Time              string `json:"time"`
	Sequence          uint64 `json:"sequence,omitempty"`
	Component         string `json:"component"`
	Event             string `json:"event"`
	Reason            string `json:"reason,omitempty"`
	Operation         string `json:"operation,omitempty"`
	Connection        uint64 `json:"connection,omitempty"`
	Request           uint64 `json:"request,omitempty"`
	DurationMs        int64  `json:"durationMs,omitempty"`
	BytesRead         uint64 `json:"bytesRead,omitempty"`
	BytesWritten      uint64 `json:"bytesWritten,omitempty"`
	Status            int    `json:"status,omitempty"`
	Allowed           *bool  `json:"allowed,omitempty"`
	Retryable         *bool  `json:"retryable,omitempty"`
	ReadIdleMs        int64  `json:"readIdleMs,omitempty"`
	WriteIdleMs       int64  `json:"writeIdleMs,omitempty"`
	DroppedEvents     uint64 `json:"droppedEvents,omitempty"`
	MuxWriteTimeoutMs int64  `json:"muxWriteTimeoutMs,omitempty"`
	MuxKeepaliveMs    int64  `json:"muxKeepaliveMs,omitempty"`
	MuxWindowBytes    uint32 `json:"muxWindowBytes,omitempty"`
}

type Recorder struct {
	eventSequence atomic.Uint64
	component     string
	events        chan Event
	done          chan struct{}
	sequence      atomic.Uint64
	dropped       atomic.Uint64
}

var components struct {
	sync.Mutex
	recorders map[string]*Recorder
}

func For(component string) *Recorder {
	if os.Getenv("PENGUIN_REMOTE_DIAGNOSTICS") != "1" {
		return nil
	}
	switch component {
	case "frpc", "frps", "frps_auth", "gateway":
	default:
		return nil
	}
	components.Lock()
	defer components.Unlock()
	if components.recorders == nil {
		components.recorders = make(map[string]*Recorder)
	}
	if r := components.recorders[component]; r != nil {
		return r
	}
	r := New(component, os.Stdout)
	components.recorders[component] = r
	return r
}

func New(component string, out io.Writer) *Recorder {
	switch component {
	case "frpc", "frps", "frps_auth", "gateway":
	default:
		return nil
	}
	r := &Recorder{component: component, events: make(chan Event, queueCapacity), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		encoder := json.NewEncoder(out)
		reportDrops := func() {
			if n := r.dropped.Swap(0); n != 0 {
				_ = encoder.Encode(Event{Time: time.Now().UTC().Format(time.RFC3339Nano), Component: component, Event: "diagnostic_events_dropped", DroppedEvents: n})
			}
		}
		for event := range r.events {
			reportDrops()
			_ = encoder.Encode(event)
		}
		reportDrops()
	}()
	return r
}

func (r *Recorder) NextID() uint64 {
	if r == nil {
		return 0
	}
	return r.sequence.Add(1)
}

func (r *Recorder) Record(event Event) {
	if r == nil || !safeEvent(event.Event) || !safeReason(event.Reason) {
		return
	}
	switch event.Operation {
	case "", "NewProxy", "Ping", "NewWorkConn", "http", "sse", "websocket":
	default:
		return
	}
	event.Time = time.Now().UTC().Format(time.RFC3339Nano)
	event.Sequence = r.eventSequence.Add(1)
	event.Component = r.component
	select {
	case r.events <- event:
	default:
		r.dropped.Add(1)
	}
}

func safeEvent(name string) bool {
	switch name {
	case "transport_connect_failed", "server_tls_failed":
		return true
	case "authorization_upstream_failed", "authorization_upstream_response":
		return true
	case "mux_settings", "transport_close_requested", "server_control_close_requested", "server_control_replaced", "client_control_close_requested":
		return true
	case "transport_open", "transport_read_failed", "transport_write_failed", "transport_write_slow", "transport_closed", "mux_keepalive_failed", "mux_read_failed", "mux_write_failed", "mux_accept_failed", "server_ping_received", "server_pong_write", "server_control_read_failed", "server_control_write_failed", "server_heartbeat_timeout", "server_ping_authorized", "authorization_started", "authorization_completed", "gateway_response", "gateway_upstream_failed", "gateway_stream_incomplete":
		return true
	}
	return false
}

func safeReason(reason string) bool {
	switch reason {
	case "dns_failed", "tls_failed", "connection_refused":
		return true
	case "invalid_response":
		return true
	case "", "eof", "unexpected_eof", "closed", "canceled", "timeout", "connection_reset", "broken_pipe", "mux_write_timeout", "mux_keepalive_timeout", "mux_closed", "mux_stream_closed", "mux_remote_goaway", "mux_receive_window_exceeded", "other", "local_close", "stream_incomplete", "auth_rejected", "response_aborted":
		return true
	}
	return false
}

type connectionKey struct{}

func WithConnection(ctx context.Context, id uint64) context.Context {
	return context.WithValue(ctx, connectionKey{}, id)
}
func ConnectionID(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	id, _ := ctx.Value(connectionKey{}).(uint64)
	return id
}
