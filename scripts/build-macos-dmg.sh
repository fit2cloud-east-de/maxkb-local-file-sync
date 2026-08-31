#!/usr/bin/env bash
set -euo pipefail

# Build a standard drag-and-drop macOS DMG.
# Optional release signing:
#   CODESIGN_IDENTITY="Developer ID Application: ..." ./scripts/build-macos-dmg.sh
#   NOTARY_PROFILE="profile-name" ./scripts/build-macos-dmg.sh

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="MaxKB 本地文件同步工具"
CONFIG_VERSION="$(python3 - <<'PYCONFIG'
import json
from pathlib import Path
print(json.loads(Path("wails.json").read_text())["info"]["productVersion"])
PYCONFIG
)"
VERSION="${APP_VERSION:-${CONFIG_VERSION}}"
if [[ "${VERSION}" != "${CONFIG_VERSION}" ]]; then
  echo "APP_VERSION=${VERSION} does not match wails.json productVersion=${CONFIG_VERSION}" >&2
  exit 2
fi
ARCH="${MACOS_ARCH:-arm64}"
case "${ARCH}" in
  x64) WAILS_ARCH="amd64" ;;
  arm64) WAILS_ARCH="arm64" ;;
  *) echo "Unsupported MACOS_ARCH=${ARCH}; expected x64 or arm64." >&2; exit 2 ;;
esac
RELEASE_NAME="MaxKB-Local-File-Sync-v${VERSION}-macos-${ARCH}"
DIST_DIR="${ROOT_DIR}/dist/macos"
DMG_PATH="${DIST_DIR}/${RELEASE_NAME}.dmg"
APP_PATH="${ROOT_DIR}/build/bin/${APP_NAME}.app"
STAGING_DIR="$(mktemp -d "${TMPDIR:-/tmp}/maxkb-dmg.XXXXXX")"

cleanup() {
  rm -rf "${STAGING_DIR}"
}
trap cleanup EXIT

mkdir -p "${DIST_DIR}" "${ROOT_DIR}/dist/checksums"
rm -f "${DMG_PATH}"

wails build -clean -platform "darwin/${WAILS_ARCH}" -trimpath \
  -ldflags "-s -w -X main.appVersion=v${VERSION}" \
  -o "${APP_NAME}"

if [[ ! -d "${APP_PATH}" ]]; then
  echo "Wails build did not produce ${APP_PATH}" >&2
  exit 1
fi

if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
  codesign --deep --force --verbose --options runtime --timestamp \
    --entitlements "${ROOT_DIR}/build/darwin/entitlements.plist" \
    --sign "${CODESIGN_IDENTITY}" "${APP_PATH}"
  codesign --verify --deep --strict --verbose=2 "${APP_PATH}"
fi

# A symlink creates the native Finder drag target without requiring a .pkg installer.
cp -R "${APP_PATH}" "${STAGING_DIR}/"
ln -s /Applications "${STAGING_DIR}/Applications"
cat > "${STAGING_DIR}/安装说明.txt" <<'TEXT'
MaxKB 本地文件同步工具安装说明

1. 双击打开此 DMG 文件。
2. 将 MaxKB 本地文件同步工具拖拽到 Applications（应用程序）。
3. 等待复制完成后关闭窗口。
4. 在 Finder 侧边栏中推出该 DMG 镜像。
5. 前往“应用程序”，双击 MaxKB 本地文件同步工具即可使用。
TEXT

hdiutil create -volname "${APP_NAME}" -srcfolder "${STAGING_DIR}" \
  -ov -format UDZO "${DMG_PATH}"

if [[ -n "${DMG_SIGN_IDENTITY:-${CODESIGN_IDENTITY:-}}" ]]; then
  codesign --force --verbose --timestamp \
    --sign "${DMG_SIGN_IDENTITY:-${CODESIGN_IDENTITY}}" "${DMG_PATH}"
  codesign --verify --verbose=2 "${DMG_PATH}"
fi

if [[ -n "${NOTARY_PROFILE:-}" ]]; then
  xcrun notarytool submit "${DMG_PATH}" --keychain-profile "${NOTARY_PROFILE}" --wait
  xcrun stapler staple "${DMG_PATH}"
  xcrun stapler validate "${DMG_PATH}"
fi

shasum -a 256 "${DMG_PATH}" | sed "s#${ROOT_DIR}/##" > "${ROOT_DIR}/dist/checksums/${RELEASE_NAME}.dmg.sha256"
printf 'Created: %s\n' "${DMG_PATH}"
printf 'SHA-256: %s\n' "${ROOT_DIR}/dist/checksums/${RELEASE_NAME}.dmg.sha256"
