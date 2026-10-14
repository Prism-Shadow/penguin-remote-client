# Desktop Releases

Official release builds target `https://go.penguin.ooo`. The public workflow does
not expose a staging environment option. Developer-only local builds can still
set `PENGUIN_REMOTE_GO_URL`; they must not be uploaded as official release assets.

## Procedure

1. Set `desktop/package.json` to the intended version. Review any dependency
   changes and update the third-party inventory if needed.
2. Commit and push the reviewed source to `main`.
3. Push a matching `desktop-v<version>` tag pointing to that commit.
4. The workflow tests the source, including Linux Go race checks, and builds
   Windows x64, macOS x64/arm64 and Linux x64 on matching runners.
5. Each build verifies the embedded service address, native binary and notices.
6. Only a complete matrix prepares a draft prerelease, uploads all seven required
   installer/archive assets plus checksums and source manifest, then publishes it.

Pull requests and main pushes run checks, not releases. Manual workflow runs
build downloadable Actions artifacts but do not publish a GitHub Release.
Release-write permission is limited to the final publishing job. No signing
secret is used in this unsigned pipeline.

An existing published version is never overwritten. A failed draft upload may
be retried only for the same source commit. All tag/version and asset-completeness
gates run before publication; failed platform builds leave no published release.

## Acceptance Boundaries

The first releases are prereleases without trusted code signing or notarization.
macOS may apply ad-hoc signatures required by its toolchain; these are not a
Developer ID signature. OS trust prompts are expected. Add verified signing and
notarization in a later version rather than replacing an existing unsigned one.

Successful packaging and the native help smoke test are not full installation,
desktop UI, network or production-service acceptance. Complete platform install
testing and paired Penguin Go/frps/Gateway deployment verification before making
the feature generally available. Existing unbound desktop credentials require
a fresh sign-in. The client does not deploy any server service.
