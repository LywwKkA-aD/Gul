#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/gul-licenses-test.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT

output_dir="$test_root/legal"

mkdir -p "$test_root/must-not-delete"
touch "$test_root/must-not-delete/sentinel"
if "$repo_root/scripts/collect-licenses.sh" "$test_root/must-not-delete" >/dev/null 2>&1; then
  echo "collector accepted an unsafe output basename" >&2
  exit 1
fi
test -f "$test_root/must-not-delete/sentinel"

mkdir -p "$output_dir"
touch "$output_dir/foreign-sentinel"
if "$repo_root/scripts/collect-licenses.sh" "$output_dir" >/dev/null 2>&1; then
  echo "collector replaced an unmarked directory" >&2
  exit 1
fi
test -f "$output_dir/foreign-sentinel"
rm "$output_dir/foreign-sentinel"
rmdir "$output_dir"

fake_go_bin="$test_root/fake-go-bin"
download_marker="$test_root/godbus-download-invoked"
platform_union_output="$test_root/platform-union/legal"
real_go=$(command -v go)
mkdir -p "$fake_go_bin" "$(dirname "$platform_union_output")"
cat >"$fake_go_bin/go" <<'FAKE_GO'
#!/usr/bin/env bash

set -euo pipefail

if [[ "${1:-}" == "list" && "$*" == *"github.com/godbus/dbus/v5"* ]]; then
  # Reproduce `go list -m` on a platform where the selected module is not in
  # the active build graph: the version is known, but .Dir is empty.
  printf 'v5.2.2|\n'
  exit 0
fi

if [[ "${1:-}" == "mod" && "${2:-}" == "download" && "$*" == *"github.com/godbus/dbus/v5"* ]]; then
  : >"$GUL_TEST_DOWNLOAD_MARKER"
fi

exec "$GUL_TEST_REAL_GO" "$@"
FAKE_GO
chmod +x "$fake_go_bin/go"

GUL_TEST_DOWNLOAD_MARKER="$download_marker" \
GUL_TEST_REAL_GO="$real_go" \
PATH="$fake_go_bin:$PATH" \
  "$repo_root/scripts/collect-licenses.sh" "$platform_union_output"
test -f "$download_marker"
test -f "$platform_union_output/THIRD_PARTY_LICENSES/go/github.com/godbus/dbus/v5/LICENSE"
grep -Fq 'github.com/godbus/dbus/v5@v5.2.2' \
  "$platform_union_output/THIRD_PARTY_MANIFEST.txt"

"$repo_root/scripts/collect-licenses.sh" "$output_dir"
"$repo_root/scripts/collect-licenses.sh" "$output_dir"

test -f "$output_dir/LICENSE"
test -f "$output_dir/copyright"
test -f "$output_dir/NOTICE"
test -f "$output_dir/THIRD_PARTY_MANIFEST.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/opus/COPYING"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/webrtc-apm/webrtc/third_party/pffft/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/wails-attributions/Chromium-LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/wails-attributions/winc-LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/wails-attributions/w32-LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/wails-attributions/atotto-clipboard-LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/toolchain-runtime/gcc/COPYING3"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/toolchain-runtime/gcc/COPYING.RUNTIME"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/toolchain-runtime/mingw-w64/COPYING.MinGW-w64-runtime.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/toolchain-runtime/mingw-w64/COPYING.MinGW-w64-runtime-upstream-v13.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/toolchain-runtime/winpthreads/COPYING"
test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/third_party/toolchain-runtime/VERSION"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/toolchain/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/LywwKkA-aD/gumble/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/LywwKkA-aD/gumble/gumble/proto/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/coder/websocket/LICENSE.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/godbus/dbus/v5/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/golang.org/x/sys/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/wailsapp/wails/v3/internal/webview2/webviewloader/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/wailsapp/wails/v3/internal/go-common-file-dialog/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/wailsapp/wails/v3/internal/assetserver/ringqueue-LICENSE-and-source.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/wailsapp/wails/v3/pkg/w32/clipboard-LICENSE-and-source.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/wailsapp/wails/v3/pkg/application/fyne-io-systray-LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/wailsapp/wails/v3/pkg/application/Chromium-NOTICE-and-source.txt"
test -f "$output_dir/THIRD_PARTY_LICENSES/npm/react/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/npm/@fontsource/ibm-plex-sans/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/npm/vite/LICENSE.md"
test -f "$output_dir/THIRD_PARTY_LICENSES/npm/@wailsio/runtime/NanoID-LICENSE-and-source.js"
test -f "$output_dir/THIRD_PARTY_LICENSES/npm/@wailsio/runtime/is-callable-LICENSE-and-source.js"
test -f "$output_dir/THIRD_PARTY_LICENSES/npm/@wailsio/runtime/HTMX-LICENSE-and-source.js"

grep -Fq 'github.com/LywwKkA-aD/gumble' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'github.com/coder/websocket@v1.8.15' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'Go toolchain' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'react@19.2.8' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq '@fontsource/ibm-plex-sans@5.3.0' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'third_party/toolchain-runtime/gcc/COPYING.RUNTIME' "$output_dir/THIRD_PARTY_MANIFEST.txt"

if find "$output_dir" -type l -print -quit | grep -q .; then
  echo "license bundle must contain regular files, not symlinks" >&2
  exit 1
fi
if find "$output_dir" -type f -name '*.go' -print -quit | grep -q .; then
  echo "licensed Go source must use .txt so go mod tidy ignores the bundle" >&2
  exit 1
fi

echo "license bundle: ok"
