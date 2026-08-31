#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_PATH="${ROOT_DIR}/build/bin/MaxKB 本地文件同步工具.app"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "This verifier currently performs macOS artifact checks only." >&2
  exit 2
fi

if [[ ! -d "${APP_PATH}" ]]; then
  echo "Missing app bundle: ${APP_PATH}" >&2
  exit 1
fi

/usr/libexec/PlistBuddy -c 'Print :CFBundleDisplayName' "${APP_PATH}/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "${APP_PATH}/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${APP_PATH}/Contents/Info.plist"

if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
  codesign --verify --deep --strict --verbose=2 "${APP_PATH}"
fi

echo "macOS app bundle metadata verification passed."
