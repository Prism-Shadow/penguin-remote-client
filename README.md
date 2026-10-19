# Penguin Remote Client

Penguin Remote connects a locally running Penguin Harness to the official
remote-access service. The native Go client embeds frpc; users do not need to
install a separate frpc executable. The Electron shell provides the desktop UI.

This repository contains client code only. Penguin Go, Gateway and the frps
authorization plugin are separate services. Opening the client source does not
grant access to the official service or replace server-side subscription and
authorization checks.

## Layout

- `cmd/penguin-remote`: CLI, loopback UI and login callback.
- `internal`: discovery, authentication, slot lifecycle, Harness session exchange,
  local HTTP forwarding and diagnostics.
- `desktop`: Electron shell, system networking bridge and platform packaging.
- `third_party/frp`: complete frp v0.71.0 source with the Penguin Remote adapter.
- `.github/workflows/desktop-build.yml`: platform build and artifact upload.

The vendored frp also includes its server implementation. This does not include
the separate Penguin Remote Gateway or authorization plugin.

## Development

Requirements: Go 1.25, Node.js 24 and pnpm 10.28.2.

```sh
go test -mod=readonly -buildvcs=false ./...
pnpm --dir desktop install --frozen-lockfile
pnpm --dir desktop test
pnpm --dir desktop typecheck
pnpm --dir desktop start
```

`start` builds the native client and starts Electron. Use
`go run ./cmd/penguin-remote help` for standalone CLI commands. No private sibling
repository, real user configuration or server credentials are build inputs.

## Packaging

```sh
pnpm --dir desktop pack:win
pnpm --dir desktop pack:mac:x64
pnpm --dir desktop pack:mac:arm64
pnpm --dir desktop pack:linux
```

Run platform packaging on its matching operating system. Build output is placed
under `dist/desktop`. `PENGUIN_REMOTE_GO_URL` selects the public service address
at build time. Standard versions default to `https://go.penguin.ooo`; explicit
`-staging.N` versions default to the staging endpoint recorded in the release
manifest. The address is not a credential.
Do not include user tokens, local configuration, signing keys or server secrets.

Production distribution builds use the production service. Explicit staging
prereleases are marked as test-only and never become Latest. There is no end-user
environment switch. Desktop
credentials, installation identity and cancellation journals are isolated under
a service-specific directory. Native product configs also record their Penguin
Go service identity and reject credentials from another service or an unbound
legacy config. Upgrading a legacy desktop installation requires a fresh sign-in;
the old state files are not automatically imported or deleted.

HTTP gzip remains fixed at level 1. Ordinary JSON forwarding keeps its bounded
64 KiB / 25 ms buffer. Control and work connections remain separate; frp tunnel
compression is disabled. Desktop networking continues to follow system policy.

## Harness sign-in compatibility / Harness 登录兼容

Remote verifies the local `api-token` against the selected Harness before
probing `POST /api/remote/session`. A successful exchange keeps automatic browser
sign-in. A direct HTTP 404 or 405 with a valid JSON API object selects manual
sign-in: pass Penguin Go authorization at the Gateway, then sign in on the
public Harness page with a Harness account. Upgrading Harness enables automatic
sign-in but is not required to start access.

Missing or invalid tokens, an explicit data directory that does not match,
401/403, transport failures, timeouts, 5xx, redirects, HTML, invalid JSON and
invalid sessions remain errors. The API client never follows redirects or uses
a browser cookie jar. An explicit data directory is checked on its own; leaving
it blank retains discovery of common data directories.

The login mode lasts for one access intent, including automatic reconnection.
Automatic mode never silently falls back after an exchange or validation error.
Manual mode makes no session exchange or validation calls while forwarding,
injects no local credentials, and preserves browser login/logout, Cookie,
Set-Cookie, REST, SSE and WebSocket requests. Logging out in manual mode does not
trigger automatic sign-in. Stop and turn access on again to detect capability
changes. Host/Origin handling and all Gateway, subscription, slot and frps
authorization checks retain their existing behavior.

Remote 先使用所选 Harness 的本地 `api-token` 验证连接，再探测
`POST /api/remote/session`。换票成功时保持自动登录；只有接口直接返回
404/405，且响应是有效的 JSON API 对象时，才允许手动登录模式开启穿透。
用户通过 Gateway 的 Penguin Go 鉴权后，在公网 Harness 页面使用 Harness
自身账号登录。升级 Harness 可获得自动登录能力，但不是开启穿透的条件。

token 缺失或无效、显式数据目录不匹配、401/403、超时、连接失败、5xx、
重定向、HTML、无效 JSON 和无效 session 均保持错误。手动模式不换票或
校验浏览器 session，不注入本地凭证；正常转发登录、退出和 Cookie，退出后
不会自动重新登录。本次开启及自动重连期间固定登录模式，停止后重新开启
会重新检测能力。现有 Host/Origin 与服务端鉴权边界保持不变。

## Extraction Status

This is an independently maintained client source project.
See [source boundaries](docs/source-boundaries.md) for provenance, shared-code
maintenance and integration-test ownership.

The workflow checks main/PR source and packages all supported targets for manual
runs or matching version tags. A successful version-tag matrix publishes a
signed Windows and notarized macOS release with checksums and a source manifest.
Stable versions are published as Latest; prerelease versions remain prereleases.
See [desktop releases](docs/releases.md).

## License

Project-owned source code and documentation are licensed under
[Apache-2.0](LICENSE). Third-party components retain their original licenses.
See [NOTICE](NOTICE), [third-party notices](THIRD_PARTY_NOTICES.md) and the original
frp license at `third_party/frp/LICENSE`. Desktop packages include these notices
under their `resources/legal` directory, together with Electron's own runtime
license files. No trademark rights or official service access are granted.
