// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/xlog"
	"github.com/hashicorp/yamux"
)

// Observe actual control I/O without changing the dispatcher's error handling.
// Messages, credentials and raw errors are deliberately excluded from logs.
type diagnosticControlReadWriter struct {
	msg.ReadWriter
	xl *xlog.Logger
}

func (rw *diagnosticControlReadWriter) ReadMsg() (msg.Message, error) {
	m, err := rw.ReadWriter.ReadMsg()
	if err != nil {
		rw.xl.Debugf("control read failed: %s", controlDiagnosticReason(err))
	}
	return m, err
}

func (rw *diagnosticControlReadWriter) WriteMsg(m msg.Message) error {
	_, heartbeat := m.(*msg.Ping)
	if heartbeat {
		rw.xl.Debugf("heartbeat write started")
	}
	started := time.Now()
	err := rw.ReadWriter.WriteMsg(m)
	if err != nil {
		rw.xl.Debugf("control write failed: %s", controlDiagnosticReason(err))
	} else if heartbeat {
		rw.xl.Debugf("heartbeat write completed duration_ms=%d", time.Since(started).Milliseconds())
	}
	return err
}

func controlDiagnosticReason(err error) string {
	for _, entry := range []struct {
		err  error
		name string
	}{
		{yamux.ErrConnectionWriteTimeout, "mux_write_timeout"},
		{yamux.ErrKeepAliveTimeout, "mux_keepalive_timeout"},
		{yamux.ErrSessionShutdown, "mux_closed"},
		{yamux.ErrStreamClosed, "mux_stream_closed"},
		{yamux.ErrRemoteGoAway, "mux_remote_goaway"},
		{yamux.ErrRecvWindowExceeded, "mux_receive_window_exceeded"},
		{yamux.ErrConnectionReset, "connection_reset"},
		{syscall.ECONNRESET, "connection_reset"},
		{syscall.EPIPE, "broken_pipe"},
		{io.EOF, "eof"},
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{net.ErrClosed, "closed"},
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "timeout"},
	} {
		if errors.Is(err, entry.err) {
			return entry.name
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "other"
}
