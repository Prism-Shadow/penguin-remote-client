// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/fatedier/frp/pkg/msg"
	frplog "github.com/fatedier/frp/pkg/util/log"
	"github.com/fatedier/frp/pkg/util/xlog"
	goliblog "github.com/fatedier/golib/log"
	"github.com/hashicorp/yamux"
)

type diagnosticReadWriterStub struct {
	readErr  error
	writeErr error
	written  msg.Message
}

func (s *diagnosticReadWriterStub) ReadMsg() (msg.Message, error) { return nil, s.readErr }
func (s *diagnosticReadWriterStub) ReadMsgInto(msg.Message) error { return s.readErr }
func (s *diagnosticReadWriterStub) WriteMsg(m msg.Message) error  { s.written = m; return s.writeErr }

func TestControlDiagnosticWrapperPreservesIOAndDoesNotLogMessages(t *testing.T) {
	var output bytes.Buffer
	original := frplog.Logger
	frplog.Logger = goliblog.New(goliblog.WithOutput(&output), goliblog.WithLevel(goliblog.DebugLevel))
	t.Cleanup(func() { frplog.Logger = original })
	stub := &diagnosticReadWriterStub{readErr: io.EOF}
	rw := &diagnosticControlReadWriter{ReadWriter: stub, xl: xlog.New()}
	ping := &msg.Ping{PrivilegeKey: "never-log-this-secret"}
	if err := rw.WriteMsg(ping); err != nil || stub.written != ping {
		t.Fatal("diagnostic wrapper changed the heartbeat write")
	}
	if _, err := rw.ReadMsg(); err != io.EOF {
		t.Fatal("diagnostic wrapper changed the read error")
	}
	privateError := errors.New("never-log-this-secret")
	stub.writeErr = privateError
	if err := rw.WriteMsg(ping); err != privateError {
		t.Fatal("diagnostic wrapper changed the write error")
	}
	for _, expected := range []string{"heartbeat write started", "heartbeat write completed duration_ms=", "control read failed: eof", "control write failed: other"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing control event %s", expected)
		}
	}
	if strings.Contains(output.String(), "never-log-this-secret") {
		t.Fatal("diagnostic wrapper exposed messages or raw errors")
	}
}

func TestControlDiagnosticReasonClassifiesWrappedFailures(t *testing.T) {
	for err, expected := range map[error]string{
		yamux.ErrKeepAliveTimeout:       "mux_keepalive_timeout",
		yamux.ErrConnectionWriteTimeout: "mux_write_timeout",
		yamux.ErrTimeout:                "timeout",
		context.DeadlineExceeded:        "timeout",
		io.ErrUnexpectedEOF:             "unexpected_eof",
	} {
		if got := controlDiagnosticReason(errors.Join(errors.New("private context"), err)); got != expected {
			t.Fatalf("classification %s, want %s", got, expected)
		}
	}
}
