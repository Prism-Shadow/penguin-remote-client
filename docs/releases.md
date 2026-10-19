# Desktop Releases

Standard release versions target `https://go.penguin.ooo`. Explicit versions in
the form `0.1.3-staging.1` target `https://107-173-122-149.sslip.io` for end-to-end
testing. The version selects the same service for all platform packages, signing
reports and the release manifest. The app has no end-user environment switch.
Developer-only local builds may override `PENGUIN_REMOTE_GO_URL`, but a mismatched
signed build cannot pass release-channel verification.

Staging releases are test-only prereleases and are never marked Latest. Configure
staging Penguin Go downloads with the explicit `releases/download/<tag>/<asset>`
URLs, not `releases/latest/download`. Existing releases remain unchanged. A later
production release needs a new version, full platform build and explicit approval;
never relabel a staging binary as a production build.

## Procedure

1. Set `desktop/package.json` to the intended version. Review any dependency
   changes and update the third-party inventory if needed.
2. Commit and push the reviewed source to `main`.
3. Push a matching `desktop-v<version>` tag pointing to that commit.
4. The workflow tests the source, including Linux Go race checks, and builds
   Windows x64, macOS x64/arm64 and Linux x64 on matching runners.
5. Each build verifies the embedded service address, native binary and notices.
   Windows releases require EVSign Authenticode signatures. Both macOS architectures
   require Developer ID signing, App notarization and stapling, and DMG notarization
   and stapling. Missing credentials or failed verification stop the release.
6. Only a complete matrix prepares a draft prerelease, uploads all seven required
   installer/archive assets plus signing verification reports, checksums and source
   manifest, then verifies the uploaded files before publication. Stable versions
   become full releases marked Latest; versions with a prerelease suffix remain
   prereleases and do not become Latest. Reports bind verified artifacts to their final
   SHA-256 digests and the source commit before the release manifest claims signing.

Pull requests and main pushes run checks, not releases. Manual workflow runs
build downloadable Actions artifacts but do not publish a GitHub Release.
Release-write permission is limited to the final publishing job. Manual runs expose
`require_windows_signing` and `require_macos_signing` inputs, both false by default;
enable them to rehearse signing without publishing. Matching `desktop-v*` tag builds
always require both. Pull request and main-branch checks do not use signing secrets.
Manual runs may set `windows_only` to test a Windows signing change without
rebuilding macOS and Linux. This option never reduces the formal tag release matrix.

## Signing Credentials

Use the same credential names and values as PenguinHarness, configured separately
in this repository's GitHub Environments:

| Environment | Secrets |
| --- | --- |
| `desktop-build` | `EVSIGN_KEY` |
| `apple-signing` | `CSC_LINK`, `CSC_KEY_PASSWORD`, `APPLE_API_KEY`, `APPLE_API_KEY_ID`, `APPLE_API_ISSUER`, `APPLE_TEAM_ID` |

`CSC_LINK` holds the Developer ID Application certificate in electron-builder's
supported format, normally a single-line Base64-encoded `.p12`. `APPLE_API_KEY`
holds the single-line Base64-encoded App Store Connect `.p8` key; CI decodes it into
a private temporary file and removes it even after failure. `APPLE_TEAM_ID` is
optional, matching Harness, and is verified against signatures when supplied.
The macOS job imports the certificate through electron-builder rather than using
a manually maintained keychain. Environment names alone do not share secrets
between repositories. Ensure environment policies permit `desktop-v*` tags and
the branches used for manual signing runs.

Windows verifies the downloaded EVSign CLI's signature before giving it a key.
The native Go executable is signed once before packaging; electron-builder signs
the desktop executable, elevation helper, NSIS uninstaller and installer. Each
signing hook checks the resulting signature, including the temporary uninstaller.
As in Harness, missing timestamps are warnings. On macOS, Gatekeeper assessment
is reported as a warning, while signature, notarization and ticket validation
failures stop the signed build. Stale DMG blockmaps are removed after stapling.

The native copy is compared to its build source before bundle signing. A local
integrity receipt tracks final bytes after signing, so packaging verification
retains source and artifact integrity checks when macOS changes the Mach-O bytes.

An existing published version is never overwritten. A failed draft upload may
be retried only for the same source commit. All tag/version and asset-completeness
gates run before publication; failed platform builds leave no published release.

## Acceptance Boundaries

Stable releases become Latest only after the full signing and upload checks pass.
Historical unsigned versions are immutable;
publish signed builds under a new version instead of replacing their assets.
Unsigned manual builds may carry ad-hoc macOS signatures; they are not Developer ID
signed or notarized and cannot pass the signed release gates. Signing does not
guarantee Windows SmartScreen reputation or complete platform installation acceptance.

Successful packaging and the native help smoke test are not full installation,
desktop UI, network or production-service acceptance. Complete platform install
testing and paired Penguin Go/frps/Gateway deployment verification before making
the feature generally available. Existing unbound desktop credentials require
a fresh sign-in. The client does not deploy any server service.
