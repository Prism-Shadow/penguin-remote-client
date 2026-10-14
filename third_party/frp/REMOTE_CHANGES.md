# Penguin Remote frp adapter

Source: github.com/fatedier/frp v0.71.0, copied from the Go module distribution.
Original LICENSE and source copyright notices are retained. The root module uses
a local replace directive; production frps must be built from this same source.

Penguin Remote modified and added Go files carry a prominent change notice,
Copyright 2026 Prism Shadow and Penguin Remote contributors, and an Apache-2.0
SPDX identifier. These notices do not replace upstream copyright or license terms.

Changes are restricted to:

- pkg/util/vhost/http.go: HTTP routing/connection failures carry an internal
  `X-Penguin-Remote-Error` marker. Gateway uses it to render the unified public
  failure page without replacing Harness application errors. frps response
  status codes and routing behavior stay unchanged.

- pkg/util/diagnostics and the client/server mux setup: opt-in, non-blocking,
  allowlisted transport/mux/control events with process-local counters and byte
  totals. The server observes Ping authorization and actual Pong writes without
  global trace logging. Events contain no business IDs, addresses or payloads.
  Message/connection results, deadlines and authorization rules are unchanged.
  The root module also carries upstream's yamux replacement so both binaries use
  the same maintained implementation.

- client/service.go and client/control.go: ProtocolError callbacks consume real
  NewProxyResp, Pong and StartWorkConn messages, including the operation name.
  Login remains alive on failure; reconnect backoff has no fast retry burst and
  caps at 30 seconds. Proxy registration retains upstream's 30-second retry.
  A lifecycle mutex also makes close-before-Run safe and prevents a canceled
  startup from subsequently running; upstream otherwise calls a nil cancel.
- pkg/util/util: a finite exact `penguin_remote:<reason>:retry|stop` vocabulary survives
  detailedErrorsToClient=false. Other errors remain hidden. No metadata or
  credentials enter the error string.
  The plugin carries Go's retryable boolean explicitly; the client does not
  replace it with a local guess based on the reason. Unsuffixed adapter-generated
  proxy_wait remains retryable. Only exact allowlisted strings are exposed.
- server/control.go: typed HTTP router conflict and existing-proxy errors become
  the retryable `proxy_wait` code. No log parsing is used.
- server/proxy: successful Manager.Add assigns a fresh lifecycle ID. The
  authenticated GET /api/remote/proxies route reads the complete live manager
  under its lock, without metrics pagination or secrets. Unrecognized proxies
  make the snapshot incomplete; no truncated snapshot may be reported.
- client/connector.go and the in-memory transport option: system bridge secrets
  are supplied as proxy authentication fields, never embedded in URLs or JSON.
  Opt-in transport diagnostics also wrap each non-multiplexed TCP connection.
  The application disables TCP multiplexing; deployment templates set the same
  server option so control heartbeats and Harness traffic use separate sockets.
- client/control_diagnostics.go: observes control reads and actual Ping writes,
  logging only fixed error categories and write durations. The application's
  opt-in allowlist also captures mux failures; I/O results and timers are unchanged.
- pkg/config/v1/client.go: no implicit http_proxy fallback. The application
  supplies its chosen route explicitly.
- pkg/transport/tls.go: system trust verification stays enabled when no custom
  CA is configured. Configure frps with a trusted certificate for serverAddr.

This adapter adds no force-disconnect API. Takeover rejects old Ping and
NewWorkConn; the old control/heartbeat lifecycle releases the HTTP domain.

Build the server from this directory with `go build -buildvcs=false -tags frps,noweb -o frps ./cmd/frps`.
For a container build set GOOS=linux, GOARCH=amd64, CGO_ENABLED=0 in the build
environment, and use the same flags. Use your normal controlled release procedure.

The noweb tag follows the upstream Makefile when dashboard dist assets are absent.
It omits the browser dashboard only; authenticated management JSON APIs remain.
