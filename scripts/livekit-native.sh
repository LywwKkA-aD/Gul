#!/usr/bin/env bash
# A separate macOS bundle keeps the lab away from Gul's running voice session.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/bin/livekit-local"
BUNDLE="$STATE/Gul LiveKit Lab.app"
[[ "$(uname -s)" == Darwin ]] || { echo 'Use GUL_LIVEKIT_LAB=1 with a native Gul build on this platform.' >&2; exit 1; }
cd "$ROOT"
GOTOOLCHAIN=go1.26.7 wails3 generate bindings -ts -i
npm --prefix frontend run build
mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Resources"
MACOSX_DEPLOYMENT_TARGET=14.0 GOTOOLCHAIN=go1.26.7 go build -tags production -ldflags '-X main.liveKitLabBuild=1' -o "$BUNDLE/Contents/MacOS/gul" .
cp build/darwin/icons.icns "$BUNDLE/Contents/Resources/"
cp build/darwin/Info.dev.plist "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleIdentifier io.github.lywwkkaad.gul.livekit-lab' "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :CFBundleName Gul LiveKit Lab' "$BUNDLE/Contents/Info.plist"
/usr/libexec/PlistBuddy -c 'Set :LSMinimumSystemVersion 14.0' "$BUNDLE/Contents/Info.plist"
GOTOOLCHAIN=go1.26.7 bash scripts/collect-licenses.sh "$STATE/legal"
# This is a generated directory within this script's dedicated test bundle.
rm -rf -- "$BUNDLE/Contents/Resources/Legal"
mkdir -p "$BUNDLE/Contents/Resources/Legal"
cp -R "$STATE/legal/." "$BUNDLE/Contents/Resources/Legal/"
codesign --force --deep --sign - "$BUNDLE"
if [[ "${1:-}" != '--build-only' ]]; then
  exec env GUL_LIVEKIT_LAB=1 "$BUNDLE/Contents/MacOS/gul"
fi
