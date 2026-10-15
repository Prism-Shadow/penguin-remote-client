#!/usr/bin/env bash
set -euo pipefail

arch="${1:?Usage: verify-macos-signing.sh <x64|arm64>}"
case "$arch" in
  x64) app="../dist/desktop/mac/Penguin Remote.app" ;;
  arm64) app="../dist/desktop/mac-arm64/Penguin Remote.app" ;;
  *) echo "Unsupported macOS architecture: $arch"; exit 1 ;;
esac
if [[ "${REQUIRE_MACOS_SIGNING:-false}" != "true" ]]; then
  echo "Developer ID signing and notarization are not required for this dry-run."
  exit 0
fi

verify_identity() {
  local details
  codesign --verify --deep --strict --verbose=4 "$1"
  details="$(codesign -dv --verbose=4 "$1" 2>&1)"
  printf '%s\n' "$details"
  if ! grep -q '^Authority=Developer ID Application:' <<< "$details"; then
    echo "::error title=Invalid macOS identity::Expected a Developer ID Application signature."
    exit 1
  fi
  if [[ -n "${APPLE_TEAM_ID:-}" ]] && ! grep -Fxq "TeamIdentifier=$APPLE_TEAM_ID" <<< "$details"; then
    echo "::error title=Invalid macOS identity::Team ID differs from APPLE_TEAM_ID."
    exit 1
  fi
}

verify_identity "$app"
verify_identity "$app/Contents/Resources/bin/penguin-remote"
xcrun stapler validate "$app"
if ! spctl --assess --type execute --verbose=4 "$app"; then
  echo "::warning title=macOS Gatekeeper::Assessment did not pass; inspect the output above."
fi

dmg="../dist/desktop/penguin-remote-mac-${arch}.dmg"
zip="../dist/desktop/penguin-remote-mac-${arch}.zip"
[[ -s "$dmg" && -s "$zip" ]] || { echo "Missing macOS DMG or ZIP"; exit 1; }
xcrun notarytool submit "$dmg" \
  --key "$APPLE_API_KEY" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER" \
  --wait --output-format json > "$RUNNER_TEMP/penguin-remote-notarization.json"
node --input-type=module -e '
  import fs from "node:fs";
  import path from "node:path";
  const result = JSON.parse(fs.readFileSync(path.join(process.env.RUNNER_TEMP, "penguin-remote-notarization.json"), "utf8"));
  console.log("DMG notarization status: " + result.status);
  if (result.status !== "Accepted") throw new Error("Apple did not accept the DMG notarization submission");
'
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
# Stapling changes DMG bytes; a previously generated blockmap is no longer valid.
rm -f "$dmg.blockmap"
node scripts/write-signing-report.mjs mac "$arch"
