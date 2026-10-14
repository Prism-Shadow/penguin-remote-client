# Source Boundaries

## Origin

Client source was extracted from Penguin Remote commit
`02bb55a50e7c20f72c0fca9e7a43d43ca71221a1` on 2026-10-14.
Only tracked client source, resources, desktop build files, module files and the
vendored frp distribution were copied. Private Git history, runtime data,
deployment configuration and credentials were not imported.

The root module is now `github.com/Prism-Shadow/penguin-remote-client`.
Internal imports changed to this module path. The frp module path and both the
frp and yamux replacements remain unchanged.

## Client Checks

Client unit tests remain here, including local discovery, durable acquire
cancellation, authentication, safe diagnostics, ordinary response buffering,
level-1 gzip, session exchange, HTML/JSON failure output and desktop helpers.

Six product integration tests remain in the private service project:

- `internal/frpclient/buffered_proxy_test.go`
- `internal/frpclient/http_cancel_test.go`
- `internal/frpclient/live_test.go`
- `internal/frpclient/live_cancel_test.go`
- `internal/frpclient/split_transport_test.go`
- `internal/frpclient/wire_test.go`

They depend on the private Gateway or authorization plugin. Their absence here
is not an integration-test pass. New client unit tests belong here; paired
frpc/frps/Gateway/Go verification still requires the private integration suite.

## Shared Source

The initial shared sources are `internal/remoteapi`, `internal/publicerror` and
`third_party/frp`. The client project is the intended maintenance source; the
new `penguin-remote-server` project retains fixed shared-source copies. The
original `penguin-remote` project keeps its unchanged client/server copies and
integration harness during the transition.

For a shared change, update the client source and the corresponding server
copy deliberately, record the counterpart revision, and run both projects'
checks. Pushing both repositories does not synchronize files automatically and
does not deploy either service. Avoid two independently diverging frp adapters.

Different Go modules cannot directly import each other's `internal` packages.
Keep fixed source copies for this first stage rather than introducing a new
shared module. If a source change touches the Penguin Go contract, coordinate
the Go project as well. Server compatibility must be ready before distributing
a client that requires it.

The original service project has not yet been pruned. Its passing integration
tests exercise its recorded client copy, not automatically this independent
client snapshot. A later cleanup must explicitly adapt that test harness before
removing the old client code or changing server dependency paths.

## Publication Boundaries

The public client repository is `Prism-Shadow/penguin-remote-client`. Its first
commit starts fresh history; private Git history is not imported. Project-owned
source is Apache-2.0, with third-party notices retained separately. Runtime
configuration, user credentials, local build output and signing keys are not
source inputs. Code publication is separate from a GitHub Release, signature,
cross-platform installation acceptance or server deployment.
