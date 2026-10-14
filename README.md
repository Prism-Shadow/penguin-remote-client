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
at build time and defaults to `https://go.penguin.ooo`. It is not a credential.
Do not include user tokens, local configuration, signing keys or server secrets.

Official distribution builds use the production service. There is no end-user
environment switch; staging addresses are developer build inputs only. Desktop
credentials, installation identity and cancellation journals are isolated under
a service-specific directory. Native product configs also record their Penguin
Go service identity and reject credentials from another service or an unbound
legacy config. Upgrading a legacy desktop installation requires a fresh sign-in;
the old state files are not automatically imported or deleted.

HTTP gzip remains fixed at level 1. Ordinary JSON forwarding keeps its bounded
64 KiB / 25 ms buffer. Control and work connections remain separate; frp tunnel
compression is disabled. Desktop networking continues to follow system policy.

## Extraction Status

This is an independently maintained client source project, not a signed release.
See [source boundaries](docs/source-boundaries.md) for provenance, shared-code
maintenance and integration-test ownership.

The workflow checks main/PR source and packages all supported targets for manual
runs or matching version tags. A successful version-tag matrix publishes an
unsigned prerelease with checksums and a source manifest. See
[desktop releases](docs/releases.md). Signing and macOS notarization are not configured.

## License

Project-owned source code and documentation are licensed under
[Apache-2.0](LICENSE). Third-party components retain their original licenses.
See [NOTICE](NOTICE), [third-party notices](THIRD_PARTY_NOTICES.md) and the original
frp license at `third_party/frp/LICENSE`. Desktop packages include these notices
under their `resources/legal` directory, together with Electron's own runtime
license files. No trademark rights or official service access are granted.
