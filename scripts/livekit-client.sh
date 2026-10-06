#!/usr/bin/env bash
# Build the complete local LiveKit client with isolated settings and bundle ID.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/bin/livekit-local"
BUNDLE="$STATE/Gul LiveKit.app"
[[ "$(uname -s)" == Darwin ]] || { echo 'Build the native Gul client with the platform Taskfile.' >&2; exit 1; }
cd "$ROOT"
GOTOOLCHAIN=go1.26.7 wails3 generate bindings -ts -i
npm --prefix frontend run build
mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Resources"
MACOSX_DEPLOYMENT_TARGET=14.0 GOTOOLCHAIN=go1.26.7 go build -tags production -o "$BUNDLE/Contents/MacOS/gul" .
cp build/darwin/icons.icns "$BUNDLE/Contents/Resources/"
cp build/darwin/Info.dev.plist "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleIdentifier io.github.lywwkkaad.gul.livekit' "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleName Gul LiveKit' "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :LSMinimumSystemVersion 14.0' "$BUNDLE/Contents/Info.plist"
GOTOOLCHAIN=go1.26.7 bash scripts/collect-licenses.sh "$STATE/legal"
# This is a generated directory within this script's dedicated test bundle.
rm -rf -- "$BUNDLE/Contents/Resources/Legal"
mkdir -p "$BUNDLE/Contents/Resources/Legal"
cp -R "$STATE/legal/." "$BUNDLE/Contents/Resources/Legal/"
codesign --force --deep --sign - "$BUNDLE"
if [[ "${1:-}" != '--build-only' ]]; then
  exec "$BUNDLE/Contents/MacOS/gul"
fi
