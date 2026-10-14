// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/util/diagnostics"
)

type controlDiagnosticEvents chan diagnostics.Event

func (events controlDiagnosticEvents) Write(p []byte) (int, error) {
	var e diagnostics.Event
	if err := json.Unmarshal(p, &e); err != nil {
		return 0, err
	}
	events <- e
	return len(p), nil
}

type diagnosticTestIO struct {
	message  msg.Message
	readErr  error
	writeErr error
}

func (rw *diagnosticTestIO) ReadMsg() (msg.Message, error) { return rw.message, rw.readErr }
func (rw *diagnosticTestIO) ReadMsgInto(msg.Message) error { return rw.readErr }
func (rw *diagnosticTestIO) WriteMsg(msg.Message) error    { return rw.writeErr }

func TestServerControlDiagnosticsPreserveIOAndExcludeCredentials(t *testing.T) {
	events := make(controlDiagnosticEvents, 8)
	ping := &msg.Ping{PrivilegeKey: "secret-credential"}
	rw := &diagnosticControlIO{ReadWriter: &diagnosticTestIO{message: ping}, recorder: diagnostics.New("frps", events), connection: 2}
	actual, err := rw.ReadMsg()
	if err != nil || actual != ping {
		t.Fatal("control read changed")
	}
	if err := rw.WriteMsg(&msg.Pong{Error: "secret-token raw-server-error"}); err != nil {
		t.Fatal("control write changed")
	}
	for _, name := range []string{"server_ping_received", "server_pong_write"} {
		select {
		case event := <-events:
			if event.Event != name || event.Connection != 2 {
				t.Fatal("incorrect control diagnostic")
			}
			data, _ := json.Marshal(event)
			if strings.Contains(string(data), "secret") {
				t.Fatal("credential leaked")
			}
		case <-time.After(time.Second):
			t.Fatal("missing event")
		}
	}
	readFailure := errors.New("private credential message")
	underlying := rw.ReadWriter.(*diagnosticTestIO)
	underlying.readErr = readFailure
	_, err = rw.ReadMsg()
	if err != readFailure {
		t.Fatal("original read error was lost")
	}
	underlying.writeErr = io.ErrClosedPipe
	if err := rw.WriteMsg(&msg.Pong{}); err != io.ErrClosedPipe {
		t.Fatal("original write error was lost")
	}
}
