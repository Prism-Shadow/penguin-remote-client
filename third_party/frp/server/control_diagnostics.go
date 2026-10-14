// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"time"

	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/diagnostics"
)

type diagnosticControlIO struct {
	msg.ReadWriter
	recorder   *diagnostics.Recorder
	connection uint64
}

func diagnosticMessageIO(ctx context.Context, rw msg.ReadWriter) msg.ReadWriter {
	r := diagnostics.For("frps")
	if r == nil {
		return rw
	}
	return &diagnosticControlIO{ReadWriter: rw, recorder: r, connection: diagnostics.ConnectionID(ctx)}
}

func (rw *diagnosticControlIO) ReadMsg() (msg.Message, error) {
	m, err := rw.ReadWriter.ReadMsg()
	if err != nil {
		rw.recorder.Record(diagnostics.Event{Event: "server_control_read_failed", Connection: rw.connection, Reason: diagnostics.Reason(err)})
	} else if _, ping := m.(*msg.Ping); ping {
		rw.recorder.Record(diagnostics.Event{Event: "server_ping_received", Connection: rw.connection})
	}
	return m, err
}

func (rw *diagnosticControlIO) WriteMsg(m msg.Message) error {
	start := time.Now()
	err := rw.ReadWriter.WriteMsg(m)
	if pong, ok := m.(*msg.Pong); ok {
		allowed := pong.Error == ""
		reason := diagnostics.Reason(err)
		if err == nil && !allowed {
			reason = "auth_rejected"
		}
		rw.recorder.Record(diagnostics.Event{Event: "server_pong_write", Connection: rw.connection, DurationMs: time.Since(start).Milliseconds(), Reason: reason, Allowed: &allowed})
	} else if err != nil {
		rw.recorder.Record(diagnostics.Event{Event: "server_control_write_failed", Connection: rw.connection, Reason: diagnostics.Reason(err)})
	}
	return err
}
