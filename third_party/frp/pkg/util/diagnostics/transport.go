// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package diagnostics

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
)

func Reason(err error) string {
	if err == nil {
		return ""
	}
	for _, item := range []struct {
		err  error
		name string
	}{
		{yamux.ErrConnectionWriteTimeout, "mux_write_timeout"}, {yamux.ErrKeepAliveTimeout, "mux_keepalive_timeout"},
		{yamux.ErrSessionShutdown, "mux_closed"}, {yamux.ErrStreamClosed, "mux_stream_closed"},
		{yamux.ErrRemoteGoAway, "mux_remote_goaway"}, {yamux.ErrRecvWindowExceeded, "mux_receive_window_exceeded"},
		{yamux.ErrConnectionReset, "connection_reset"}, {syscall.ECONNRESET, "connection_reset"}, {syscall.EPIPE, "broken_pipe"},
		{io.EOF, "eof"}, {io.ErrUnexpectedEOF, "unexpected_eof"}, {net.ErrClosed, "closed"},
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "timeout"},
		{syscall.ECONNREFUSED, "connection_refused"},
	} {
		if errors.Is(err, item.err) {
			return item.name
		}
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && !dns.Timeout() {
		return "dns_failed"
	}
	var certificate x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	if errors.As(err, &certificate) || errors.As(err, &hostname) || errors.As(err, &invalidCertificate) {
		return "tls_failed"
	}
	var e net.Error
	if errors.As(err, &e) && e.Timeout() {
		return "timeout"
	}
	return "other"
}

type tracedConn struct {
	net.Conn
	recorder       *Recorder
	id             uint64
	opened         time.Time
	readBytes      atomic.Uint64
	writeBytes     atomic.Uint64
	lastRead       atomic.Int64
	lastWrite      atomic.Int64
	closeOnce      sync.Once
	closeRequested sync.Once
}

func WrapConn(ctx context.Context, conn net.Conn, recorder *Recorder) (context.Context, net.Conn) {
	if recorder == nil {
		return ctx, conn
	}
	c := &tracedConn{Conn: conn, recorder: recorder, id: recorder.NextID(), opened: time.Now()}
	c.lastRead.Store(c.opened.UnixNano())
	c.lastWrite.Store(c.opened.UnixNano())
	recorder.Record(Event{Event: "transport_open", Connection: c.id})
	return WithConnection(ctx, c.id), c
}

func (c *tracedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.readBytes.Add(uint64(n))
		c.lastRead.Store(time.Now().UnixNano())
	}
	if err != nil {
		c.recorder.Record(c.snapshot("transport_read_failed", Reason(err), 0))
	}
	return n, err
}

func (c *tracedConn) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := c.Conn.Write(p)
	duration := time.Since(start).Milliseconds()
	if n > 0 {
		c.writeBytes.Add(uint64(n))
		c.lastWrite.Store(time.Now().UnixNano())
	}
	if err != nil {
		c.recorder.Record(c.snapshot("transport_write_failed", Reason(err), duration))
	} else if duration >= 250 {
		c.recorder.Record(c.snapshot("transport_write_slow", "", duration))
	}
	return n, err
}

func (c *tracedConn) snapshot(event, reason string, duration int64) Event {
	now := time.Now()
	return Event{Event: event, Reason: reason, Connection: c.id, DurationMs: duration, BytesRead: c.readBytes.Load(), BytesWritten: c.writeBytes.Load(), ReadIdleMs: now.Sub(time.Unix(0, c.lastRead.Load())).Milliseconds(), WriteIdleMs: now.Sub(time.Unix(0, c.lastWrite.Load())).Milliseconds()}
}

func (c *tracedConn) Close() error {
	c.closeRequested.Do(func() { c.recorder.Record(c.snapshot("transport_close_requested", "local_close", 0)) })
	err := c.Conn.Close()
	c.closeOnce.Do(func() {
		c.recorder.Record(c.snapshot("transport_closed", "local_close", time.Since(c.opened).Milliseconds()))
	})
	return err
}

type muxWriter struct {
	recorder   *Recorder
	connection uint64
}

func MuxWriter(ctx context.Context, recorder *Recorder, fallback io.Writer) io.Writer {
	if recorder == nil {
		return fallback
	}
	return muxWriter{recorder: recorder, connection: ConnectionID(ctx)}
}
func (w muxWriter) Write(p []byte) (int, error) {
	if len(p) > 64<<10 {
		return len(p), nil
	}
	message := string(p)
	for _, entry := range []struct{ marker, event string }{
		{"yamux: keepalive failed: ", "mux_keepalive_failed"}, {"yamux: Failed to write header: ", "mux_write_failed"},
		{"yamux: Failed to write body: ", "mux_write_failed"}, {"yamux: Failed to read header: ", "mux_read_failed"},
		{"yamux: Failed to read stream data: ", "mux_read_failed"},
	} {
		if index := strings.Index(message, entry.marker); index >= 0 {
			reason := strings.TrimSpace(message[index+len(entry.marker):])
			category := "other"
			switch {
			case strings.Contains(reason, "keepalive timeout"):
				category = "mux_keepalive_timeout"
			case strings.Contains(reason, "connection write timeout"):
				category = "mux_write_timeout"
			case strings.Contains(reason, "i/o timeout") || strings.Contains(reason, "i/o deadline reached"):
				category = "timeout"
			case strings.Contains(reason, "connection reset"):
				category = "connection_reset"
			case strings.Contains(reason, "session shutdown"):
				category = "mux_closed"
			case reason == "EOF":
				category = "eof"
			}
			w.recorder.Record(Event{Event: entry.event, Reason: category, Connection: w.connection})
			break
		}
	}
	return len(p), nil
}
