package frpclient

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/util/log"
	goliblog "github.com/fatedier/golib/log"
)

const diagnosticsEnv = "PENGUIN_REMOTE_DIAGNOSTICS"
const diagnosticQueueCapacity = 256

var activeDiagnostics *diagnosticWriter

func configureDiagnosticLogging() {
	if os.Getenv(diagnosticsEnv) != "1" {
		log.Logger = log.Logger.WithOptions(goliblog.WithOutput(io.Discard))
		return
	}
	activeDiagnostics = newDiagnosticWriter(os.Stdout)
	log.Logger = log.Logger.WithOptions(
		goliblog.WithLevel(goliblog.TraceLevel),
		goliblog.WithOutput(activeDiagnostics),
	)
}

type diagnosticEvent struct {
	Time                     string `json:"time"`
	Component                string `json:"component,omitempty"`
	Sequence                 uint64 `json:"sequence,omitempty"`
	Event                    string `json:"event"`
	Reason                   string `json:"reason,omitempty"`
	DurationMs               *int64 `json:"durationMs,omitempty"`
	HeartbeatIntervalSeconds int64  `json:"heartbeatIntervalSeconds,omitempty"`
	HeartbeatTimeoutSeconds  int64  `json:"heartbeatTimeoutSeconds,omitempty"`
	MuxKeepaliveSeconds      int64  `json:"muxKeepaliveSeconds,omitempty"`
	TCPMux                   *bool  `json:"tcpMux,omitempty"`
	Operation                string `json:"operation,omitempty"`
	Retryable                *bool  `json:"retryable,omitempty"`
	DroppedEvents            uint64 `json:"droppedEvents,omitempty"`
}

func reportDiagnosticSettings(common *v1.ClientCommonConfig) {
	if activeDiagnostics != nil {
		activeDiagnostics.emit(diagnosticEvent{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "transport_settings", HeartbeatIntervalSeconds: common.Transport.HeartbeatInterval, HeartbeatTimeoutSeconds: common.Transport.HeartbeatTimeout, MuxKeepaliveSeconds: common.Transport.TCPMuxKeepaliveInterval, TCPMux: common.Transport.TCPMux})
	}
}

func reportProtocolDiagnostic(operation, reason string, retryable bool) {
	if activeDiagnostics == nil || !remoteapi.SafeReasons[reason] {
		return
	}
	switch operation {
	case "NewProxy", "Ping", "NewWorkConn":
		activeDiagnostics.emit(diagnosticEvent{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "authorization_rejected", Operation: operation, Reason: reason, Retryable: &retryable})
	}
}

// Only fixed event names, fixed error classes and numeric durations leave this
// writer. Raw frp/yamux logs can contain credentials and must never be forwarded.
type diagnosticWriter struct {
	sequence atomic.Uint64
	out      io.Writer
	events   chan diagnosticEvent
	done     chan struct{}
	dropped  atomic.Uint64
}

var diagnosticPrefix = regexp.MustCompile(`^.*?\[(?:T|D|I|W|E)\] \[client/(?:control|control_diagnostics|connector|service)\.go:\d+\] (?:\[[^\]\r\n]*\] )*`)
var diagnosticMuxPrefix = regexp.MustCompile(`^.*?\[(?:T|D|I|W|E)\] \[xlog/log_writer\.go:\d+\] (?:\[[^\]\r\n]*\] )*`)

func newDiagnosticWriter(out io.Writer) *diagnosticWriter {
	w := &diagnosticWriter{out: out, events: make(chan diagnosticEvent, diagnosticQueueCapacity), done: make(chan struct{})}
	go w.drain()
	return w
}

func (w *diagnosticWriter) Write(p []byte) (int, error) {
	return w.WriteLog(p, goliblog.DebugLevel, time.Now())
}

func (w *diagnosticWriter) WriteLog(p []byte, _ goliblog.Level, when time.Time) (int, error) {
	if len(p) > 64<<10 {
		return len(p), nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		prefix := diagnosticPrefix.FindStringIndex(line)
		mux := false
		if prefix == nil {
			prefix = diagnosticMuxPrefix.FindStringIndex(line)
			mux = true
		}
		if prefix == nil {
			continue
		}
		event, ok := classifyDiagnostic(strings.TrimSpace(line[prefix[1]:]))
		if !ok {
			continue
		}
		if mux && event.Event != "mux_keepalive_failed" && event.Event != "mux_read_failed" && event.Event != "mux_write_failed" {
			continue
		}
		event.Time = when.UTC().Format(time.RFC3339Nano)
		w.emit(event)
	}
	// A broken log destination must not change tunnel behaviour.
	return len(p), nil
}

func (w *diagnosticWriter) emit(event diagnosticEvent) {
	event.Component = "frpc_control"
	event.Sequence = w.sequence.Add(1)
	select {
	case w.events <- event:
	default:
		w.dropped.Add(1)
	}
}

// Only this worker touches the output. A stalled destination cannot hold up
// transport goroutines; overflow is reported after output resumes.
func (w *diagnosticWriter) drain() {
	defer close(w.done)
	encoder := json.NewEncoder(w.out)
	reportDropped := func() {
		if count := w.dropped.Swap(0); count != 0 {
			_ = encoder.Encode(diagnosticEvent{Time: time.Now().UTC().Format(time.RFC3339Nano), Component: "frpc_control", Event: "diagnostic_events_dropped", DroppedEvents: count})
		}
	}
	for event := range w.events {
		reportDropped()
		_ = encoder.Encode(event)
	}
	reportDropped()
}

func classifyDiagnostic(message string) (diagnosticEvent, bool) {
	events := map[string]string{
		"try to connect to server...":       "connect_attempt",
		"send heartbeat to server":          "heartbeat_scheduled",
		"heartbeat write started":           "heartbeat_write_started",
		"receive heartbeat from server":     "heartbeat_pong_received",
		"heartbeat timeout":                 "application_heartbeat_timeout",
		"control message dispatcher exited": "control_dispatcher_exited",
	}
	if name, ok := events[message]; ok {
		return diagnosticEvent{Event: name}, true
	}
	const completed = "heartbeat write completed duration_ms="
	if strings.HasPrefix(message, completed) {
		value, err := strconv.ParseInt(strings.TrimPrefix(message, completed), 10, 64)
		if err == nil && value >= 0 {
			return diagnosticEvent{Event: "heartbeat_write_completed", DurationMs: &value}, true
		}
		return diagnosticEvent{}, false
	}
	for prefix, event := range map[string]string{
		"control read failed: ":               "control_read_failed",
		"control write failed: ":              "control_write_failed",
		"yamux: keepalive failed: ":           "mux_keepalive_failed",
		"yamux: Failed to write header: ":     "mux_write_failed",
		"yamux: Failed to write body: ":       "mux_write_failed",
		"yamux: Failed to read header: ":      "mux_read_failed",
		"yamux: Failed to read stream data: ": "mux_read_failed",
	} {
		if strings.HasPrefix(message, prefix) {
			return diagnosticEvent{Event: event, Reason: diagnosticReason(strings.TrimPrefix(message, prefix))}, true
		}
	}
	for prefix, event := range map[string]string{
		"login to server success, get run id ":    "connection_established",
		"connect to server error: ":               "connect_failed",
		"pong message contains error: ":           "heartbeat_rejected",
		"error during ping authentication: ":      "heartbeat_auth_failed",
		"work connection write to server error: ": "work_connection_write_failed",
	} {
		if strings.HasPrefix(message, prefix) {
			return diagnosticEvent{Event: event}, true
		}
	}
	return diagnosticEvent{}, false
}

func diagnosticReason(reason string) string {
	for _, value := range []string{"eof", "unexpected_eof", "closed", "canceled", "timeout", "connection_reset", "broken_pipe", "mux_write_timeout", "mux_keepalive_timeout", "mux_closed", "mux_stream_closed", "mux_remote_goaway", "mux_receive_window_exceeded", "other"} {
		if reason == value {
			return value
		}
	}
	// Yamux logs contain free-form errors. Match known vocabulary but emit none
	// of the original text, including addresses or peer-supplied messages.
	for fragment, value := range map[string]string{
		"connection write timeout": "mux_write_timeout",
		"keepalive timeout":        "mux_keepalive_timeout",
		"i/o deadline reached":     "timeout",
		"i/o timeout":              "timeout",
		"connection reset":         "connection_reset",
		"session shutdown":         "mux_closed",
	} {
		if strings.Contains(reason, fragment) {
			return value
		}
	}
	if reason == "EOF" {
		return "eof"
	}
	return "other"
}
