#!/usr/bin/env bash
set -euo pipefail

if [[ "${REQUIRE_MACOS_SIGNING:-false}" != "true" ]]; then
  echo "macOS signing is not required for this dry-run; building unsigned."
  echo "CSC_IDENTITY_AUTO_DISCOVERY=false" >> "$GITHUB_ENV"
  exit 0
fi

missing=()
for name in CSC_LINK CSC_KEY_PASSWORD APPLE_API_KEY_BASE64 APPLE_API_KEY_ID APPLE_API_ISSUER; do
  [[ -n "${!name:-}" ]] || missing+=("$name")
done
if ((${#missing[@]})); then
  printf '::error title=Missing macOS signing secrets::%s (APPLE_API_KEY_BASE64 comes from APPLE_API_KEY)\n' "${missing[*]}"
  exit 1
fi
for name in CSC_LINK CSC_KEY_PASSWORD APPLE_API_KEY_BASE64 APPLE_API_KEY_ID APPLE_API_ISSUER APPLE_TEAM_ID; do
  value="${!name:-}"
  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    echo "::error title=Invalid macOS signing secrets::$name must be a single-line value."
    exit 1
  fi
done
if [[ ! "$APPLE_API_KEY_ID" =~ ^[A-Za-z0-9]+$ ]]; then
  echo "::error title=Invalid APPLE_API_KEY_ID::Expected an alphanumeric key ID."
  exit 1
fi

umask 077
mkdir -p "$RUNNER_TEMP/penguin-remote-signing"
api_key_path="$RUNNER_TEMP/penguin-remote-signing/AuthKey_${APPLE_API_KEY_ID}.p8"
printf '%s' "$APPLE_API_KEY_BASE64" | base64 -D > "$api_key_path"
if ! grep -q "BEGIN PRIVATE KEY" "$api_key_path"; then
  echo "::error title=Invalid APPLE_API_KEY::Decoded key is not a .p8 private key."
  exit 1
fi
{
  echo "CSC_LINK=$CSC_LINK"
  echo "CSC_KEY_PASSWORD=$CSC_KEY_PASSWORD"
  echo "APPLE_API_KEY=$api_key_path"
  echo "APPLE_API_KEY_ID=$APPLE_API_KEY_ID"
  echo "APPLE_API_ISSUER=$APPLE_API_ISSUER"
  [[ -z "${APPLE_TEAM_ID:-}" ]] || echo "APPLE_TEAM_ID=$APPLE_TEAM_ID"
} >> "$GITHUB_ENV"
