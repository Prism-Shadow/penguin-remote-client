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

## Alibaba Cloud OSS Asset Mirror

After the tag release job successfully publishes the GitHub Release, its independent
`mirror-oss` job calls `oss-mirror.yml`. It downloads the published assets from GitHub
and mirrors the same bytes; it never rebuilds, signs, notarizes or modifies the Release.
It does not rely on a `release` event: GitHub does not start another workflow from a
Release created with `GITHUB_TOKEN`. PRs, ordinary main pushes and the original manual
desktop build/signing rehearsals cannot invoke the OSS publisher. A mirror failure
leaves the already published GitHub Release available and can be retried separately.
The app's UI and updater continue to use their existing download sources.

### Environment Variables and Trust

Create this repository's `oss-production` and `oss-staging` GitHub Environments.
Both must define these **Environment variables**, independently of signing secrets:

| Variable | Value or purpose |
| --- | --- |
| `ALIYUN_OIDC_PROVIDER_ARN` | RAM GitHub OIDC provider ARN |
| `ALIYUN_ROLE_ARN` | Environment-specific production or staging RAM role ARN |
| `OSS_BUCKET` | Dedicated Client bucket; suggested name `penguin-remote-client-releases` |
| `OSS_REGION` | `cn-beijing` |
| `OSS_ENDPOINT` | `oss-cn-beijing.aliyuncs.com`; alternatively `oss-accelerate.aliyuncs.com` after acceleration is enabled |
| `OSS_PUBLIC_BASE_URL` | Bucket's Beijing public HTTPS origin, for example `https://penguin-remote-client-releases.oss-cn-beijing.aliyuncs.com` |

No bucket, upload endpoint or download origin is hardcoded in the publishing scripts.
An endpoint is a hostname without a scheme; the public base URL is an HTTPS origin.
Changing the upload endpoint does not require changing the public download origin.
Use the Client bucket rather than sharing the Harness release namespace.

Configure Alibaba Cloud OIDC with issuer `https://token.actions.githubusercontent.com`
and audience/client ID `github-actions`. Restrict the roles' trust to this repository
and the matching Environment subject:
`repo:Prism-Shadow@236323912/penguin-remote-client@1418319018:environment:oss-production` or
`repo:Prism-Shadow@236323912/penguin-remote-client@1418319018:environment:oss-staging`.
Environment deployment rules should allow the intended `desktop-v*` tags and the
reviewed branch used for manual mirroring. Use Environment protection rules to
control which source may request these roles. Each OSS job alone receives
`id-token: write` and `contents: read`; the mirror has no Release-write permission.
The Aliyun authentication action is pinned to commit
`1e5248c8d5d93a8781ac344a68e19a43341e79e6` and exchanges OIDC for temporary credentials.
`install-ossutil.sh` pins ossutil 2.3.0 and verifies its archive's SHA-256 before use.
Long-lived Alibaba Cloud keys are unnecessary.

The roles need only these object permissions in the Client bucket:

| Role | Actions | Object keys |
| --- | --- | --- |
| Production | `oss:GetObject`, `oss:PutObject` | `releases/*`, `latest.json` |
| Staging | `oss:GetObject`, `oss:PutObject` | `staging/*` |

Use RAM object resource ARNs `acs:oss:*:*:<bucket>/releases/*` and
`acs:oss:*:*:<bucket>/latest.json` for production, and
`acs:oss:*:*:<bucket>/staging/*` for staging. Neither workflow needs ListObjects,
DeleteObject or bucket-management permissions. Avoid additional policies that grant
the staging role production writes. Anonymous HTTPS GetObject on `releases/*` and
`latest.json` must be configured separately for public downloads; authenticated
round-trip success alone does not prove anonymous access. The bucket may remain
private with a narrowly scoped public-read policy. Do not expose `staging/*` publicly.

### Mirror Contract, Immutability and Latest

Every asset goes under `releases/<complete desktop-v... tag>/`, including installers,
archives, signing reports, `SHA256SUMS.txt`, `release-manifest.json`, published
`latest*.yml` feeds and blockmaps. Before any OSS write, the downloaded file set, sizes
and SHA-256 digests must agree with GitHub's asset metadata. Both metadata files
themselves are checked against GitHub too. The manifest must agree with the requested
tag/version and GitHub's full `target_commitish` source SHA. Its complete asset list
and `SHA256SUMS.txt` must agree with the downloaded contents. Required signing reports
and update feeds are determined by the trusted tag/source contract, then verified.

Historical exceptions are an explicit allowlist pinned to the source commits of the
audited tags, rather than inferred from manifest flags or missing files:

| Tags | Required signing | Required update feeds |
| --- | --- | --- |
| `desktop-v0.1.0`, `desktop-v0.1.1`, `desktop-v0.1.2` | Unsigned historical contract | None |
| `desktop-v0.1.3-staging.1`, `desktop-v0.1.3`, `desktop-v0.1.4`, `desktop-v0.1.5` | Signed and notarized reports | None |
| All other tags, including `desktop-v0.1.6` and later versions | Signed and notarized reports | All three feeds |

Only `desktop-v0.1.0` accepts the original `linux-x64` installer names. Historical
tag/source mismatches fail; new prerelease names do not inherit older exceptions.
Mirrors still use the original manifest and checksums and never regenerate metadata
from the current `desktop/package.json`. Removing all feeds or changing signing
flags cannot downgrade a modern release's requirements, even if all GitHub digests
and manifest/checksum entries have been updated to match. Unsupported/inconsistent contracts or
missing GitHub SHA-256 metadata fail closed. This separate public-Release validator
does not relax `verify-uploaded-release.mjs`: publication still requires an unpublished
draft prerelease and all existing signing, notarization, feed merge and upload gates.

Version objects use `Cache-Control: public,max-age=31536000,immutable`. An exact-key
GetObject checks any existing object: equal SHA-256 means skip; different content
means failure. New PutObject requests use `--forbid-overwrite` and every newly
uploaded object is downloaded and SHA-256 checked. If another attempt wins a create
race, the resulting object must still match. Authorization and network failures on
GetObject are not treated as missing objects. Partial mirrors can be retried; there
is no rollback by deletion or overwrite. Changed assets require a new release tag.

After all files pass, a stable, non-prerelease version that is still GitHub's current
Latest may update root `latest.json`. Prereleases, staging versions and historical
non-Latest mirrors never update it. The job checks GitHub Latest before transfer and
again immediately before writing. Automatic and manual entry points share the same
`penguin-remote-client-oss-production` job concurrency group across all tags; retries
cannot race another mirror writer. GitHub concurrency is a lock, not a FIFO queue;
check each requested run's result and retry a run superseded while pending. Do not
add another pointer writer outside this group.
The pointer uses `Cache-Control: no-cache` and is read back and verified too:

```json
{
  "schemaVersion": 1,
  "tag": "desktop-v0.1.6",
  "version": "0.1.6",
  "releaseBaseUrl": "https://penguin-remote-client-releases.oss-cn-beijing.aliyuncs.com/releases/desktop-v0.1.6"
}
```

### Manual Mirror and Staging Validation

To fill or retry an existing mirror, run **Mirror existing desktop Release to OSS**
(`oss-mirror.yml`) with the full `tag`, selecting a reviewed branch containing the
mirror scripts. This dispatch only reads and downloads the existing public Release.
A missing Release, draft, invalid tag or incomplete/inconsistent asset set fails
before OIDC credentials are requested. It never invokes packaging, signing or
`gh release edit/upload/create`. A failed automatic mirror does not require rerunning
the original release build. Mirror scripts recheck GitHub metadata before OSS writes.

Run **Test desktop OSS staging permissions** (`oss-staging.yml`) to exchange the
staging role, PutObject/GetObject a unique `staging/<run>/oidc-probe.txt`, compare
contents and require AccessDenied for a unique production `releases/` key. A mistaken
policy can leave that production deny-probe object behind; fix the policy and remove
it manually using a separately authorized operator. The workflow never deletes objects.

For the exact `latest.json` denial check, staging first fetches the existing pointer
over anonymous HTTPS and submits those same bytes with `--forbid-overwrite`.
AccessDenied passes; FileAlreadyExists/409 fails because it indicates write permission
was granted. Other failures also fail rather than passing a denial check. On a new
bucket, HTTP 404 explicitly reports **PENDING / NOT EXECUTED** for this check, while
the staging round trip and `releases/` denial can pass. No fake production pointer is
created. Repeat staging validation after a real stable mirror has created `latest.json`.

Local offline coverage runs with `pnpm --dir desktop test`. Test fixtures mock GitHub,
OSS and public HTTP responses, use no real credentials and do not contact OSS.

### Real Integration Checklist — Not Yet Executed

Local tests do not verify the following. Perform these only after cloud configuration
is complete, the reviewed workflows are available on GitHub, and an operator has
explicitly authorized integration runs:

1. Check both Environments' six variables, deployment protections, bucket public-read
   scope, two roles' object policies, issuer/audience and repository/Environment OIDC
   trust. Confirm there are no broad inherited production grants on the staging role.
2. Dispatch `oss-staging.yml`. Confirm real OIDC exchange, staging byte equality and
   `releases/` AccessDenied. If the bucket has no real pointer yet, record the exact
   `latest.json` check as pending rather than fully passing permission acceptance.
3. Dispatch `oss-mirror.yml` for an existing public historical or prerelease tag.
   Compare every mirrored object's bytes/SHA-256 with the GitHub Release and confirm
   that the root pointer is not created or changed for a non-Latest/prerelease tag.
4. Mirror the real current stable Latest. Verify all version objects, anonymous
   downloads, immutable cache headers, pointer schema, `no-cache`, and matching tag.
   Rerun the same tag to verify safe idempotence. Repeat the staging pointer-denial
   check after the real `latest.json` exists.
5. During a separately approved future normal release, verify the complete signing,
   notarization, Release publication and dependent mirror chain. Exercise queued
   historical retry/new-version runs and verify GitHub Latest still controls the
   pointer. No tag push or release is needed merely to prepare this implementation.
6. If acceleration is later enabled, authorize a separate staging/mirror run with
   the accelerated upload endpoint and verify the unchanged Beijing download URLs.

Real OIDC, OSS transfers, production permission denials, cache headers, anonymous
downloads, workflow execution and concurrency behavior are **not yet executed** by
the local implementation or its offline tests.

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

Signed macOS jobs on both architectures temporarily block outgoing UDP port 443
on their ephemeral runner so Apple's ticket service uses HTTPS over TCP. This follows the
[runner maintainers' workaround](https://github.com/actions/runner-images/issues/14924)
for HTTP/3 connection drops during stapling. Signing, notarization and stapling
verification remain required. Remove the workaround when the runner issue is resolved.
The same jobs install a temporary `xcrun` wrapper before packaging, covering both
electron-builder's application stapling and the final application/DMG validation.
Only ticket transport failures and timeouts retry, up to four attempts with 5, 15
and 30 second delays. Other `xcrun` commands pass directly to the system executable;
signature failures, rejected notarization and exhausted retries still block release.

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

## Client Updates

Stable packaged clients check GitHub Releases after startup and every six hours.
Preferences shows the running version, check result and download progress. Download
and restart/install are separate user actions; ordinary quit does not install a
downloaded update. Installing restarts the desktop app and interrupts active remote
connections. Tunnel intent, authorization, cancellation and recovery logic are not
changed by the updater. Releases without an updater require one manual upgrade.

The Preferences badge marks an unviewed version. Opening Preferences dismisses it
for that version, regardless of download or installation. The receipt is stored
alongside the UI preferences and survives restarts; a newer version can raise a
new badge.

Windows x64 NSIS, macOS x64/arm64 and Linux x64 AppImage installations can update.
Development runs, preview/staging builds, unsupported architectures and Linux deb
installations use manual downloads. Stable clients never select prereleases or
automatically downgrade. Keep the `desktop-v<version>` tag convention and use a new
version for releases containing the updater; do not replace existing release assets.

Packaging declares the GitHub publish provider while retaining `--publish never`.
Each matrix job stages architecture-specific update metadata after signature and
notarization checks. The release job merges these snapshots into `latest.yml`,
`latest-mac.yml` (both ZIP architectures) and `latest-linux.yml`. DMGs remain manual
downloads and are excluded from the macOS update feed. SHA-512 and file sizes bind
the feeds to final package bytes; the release manifest and upload verification also
cover the feeds and available blockmaps. Missing or inconsistent feeds block release.

Windows uses the same Authenticode validity verification as PenguinHarness,
without pinning signing-service publisher names. Fresh and cached downloads are
verified before readiness and checked again before installation; invalid cached
installers are removed so a new check and download can recover. macOS retains
Developer ID signing and notarization requirements. Update
checks and downloads use Electron networking and its system proxy configuration.

Validate an installed older updater-enabled build against a newer signed build on
Windows, both macOS architectures and AppImage. Verify restart, installed version,
configuration retention, download retry and failure handling. Unit tests and package
verification alone do not establish actual installation acceptance.

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
