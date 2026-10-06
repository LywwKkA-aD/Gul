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

# LiveKit can use the embedded REALITY transport. Its modified MPL source
# accompanies the binary; the retired Mumble/Hysteria transports remain absent.
for covered_file in LICENSE VERSION handshake.go.txt; do
  test -f "$output_dir/THIRD_PARTY_LICENSES/vendored/internal/reality/$covered_file"
done
cmp "$repo_root/internal/reality/handshake.go" \
  "$output_dir/THIRD_PARTY_LICENSES/vendored/internal/reality/handshake.go.txt"
for retired_module in github.com/LywwKkA-aD/gumble github.com/apernet/hysteria/core/v2 github.com/apernet/hysteria/extras/v2; do
  test ! -e "$output_dir/THIRD_PARTY_LICENSES/go/$retired_module"
  if grep -Fq "  $retired_module@" "$output_dir/THIRD_PARTY_MANIFEST.txt"; then
    echo "retired runtime module was attributed as bundled: $retired_module" >&2
    exit 1
  fi
done

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
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/livekit/server-sdk-go/v2/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/livekit/protocol/LICENSE"
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/pion/webrtc/v4/LICENSE"
# A source filename matching "license" must preserve its complete contents
# without creating a package inside generated .app/Legal directories.
twirp_source_dir=$(cd "$repo_root" && go list -m -f '{{.Dir}}' github.com/twitchtv/twirp)
cmp "$twirp_source_dir/license_test.go" \
  "$output_dir/THIRD_PARTY_LICENSES/go/github.com/twitchtv/twirp/license_test.go.txt"
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

# protobuf-es 1.10.1 omits LICENSE from its npm tarball. Its Apache license
# and the complete inline Google BSD notice both have to survive minification.
protobuf_licenses="$output_dir/THIRD_PARTY_LICENSES/npm/@bufbuild/protobuf"
cmp "$repo_root/third_party/npm-attributions/bufbuild-protobuf-1.10.1/LICENSE-APACHE-2.0" \
  "$protobuf_licenses/LICENSE-APACHE-2.0"
cmp "$repo_root/frontend/node_modules/@bufbuild/protobuf/dist/esm/google/varint.js" \
  "$protobuf_licenses/Google-BSD-3-Clause-LICENSE-and-source.js"
cmp "$repo_root/frontend/node_modules/@bufbuild/protobuf/dist/esm/index.js" \
  "$protobuf_licenses/Buf-NOTICE-and-source.js"
grep -Fq 'Copyright 2008 Google Inc.' "$protobuf_licenses/Google-BSD-3-Clause-LICENSE-and-source.js"
grep -Fq 'Copyright 2021-2024 Buf Technologies, Inc.' "$protobuf_licenses/Buf-NOTICE-and-source.js"
grep -Fq 'Apache License' "$protobuf_licenses/LICENSE-APACHE-2.0"
grep -Fq 'v1.10.1' "$protobuf_licenses/VERSION"

grep -Fq 'github.com/livekit/server-sdk-go/v2@v2.18.1' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'github.com/livekit/protocol@v1.49.0' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'github.com/pion/webrtc/v4@' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'github.com/coder/websocket@v1.8.15' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'Go toolchain' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'react@19.2.8' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq '@bufbuild/protobuf@1.10.1' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'livekit-client@2.22.3' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq '@fontsource/ibm-plex-sans@5.3.0' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'third_party/toolchain-runtime/gcc/COPYING.RUNTIME' "$output_dir/THIRD_PARTY_MANIFEST.txt"

# The Go module set is derived from the build graph of every shipped target,
# so platform-specific dependencies are attributed whatever the host is.
test -f "$output_dir/THIRD_PARTY_LICENSES/go/github.com/go-ole/go-ole/LICENSE"
grep -Fq 'github.com/go-ole/go-ole@' "$output_dir/THIRD_PARTY_MANIFEST.txt"
grep -Fq 'golang.org/x/sys@' "$output_dir/THIRD_PARTY_MANIFEST.txt"

# devOptional packages (type definitions and their dependencies) belong to the
# development tree only and must not be attributed as bundled.
test ! -e "$output_dir/THIRD_PARTY_LICENSES/npm/@types"
test ! -e "$output_dir/THIRD_PARTY_LICENSES/npm/@types/dom-mediacapture-record"
test ! -e "$output_dir/THIRD_PARTY_LICENSES/npm/csstype"
if grep -Eq '^  (@types/|csstype@)' "$output_dir/THIRD_PARTY_MANIFEST.txt"; then
  echo "development-only npm packages must not be attributed as bundled" >&2
  exit 1
fi

# Modules outside the shipped build graphs must not be attributed. Neither of
# these reaches a released binary: they enter the default-tag graphs only
# (go-isatty everywhere, go-colorable on Windows). Dropping "-tags production"
# or going back to a hand-kept module array puts both back into the bundle.
test ! -e "$output_dir/THIRD_PARTY_LICENSES/go/github.com/mattn/go-colorable"
test ! -e "$output_dir/THIRD_PARTY_LICENSES/go/github.com/mattn/go-isatty"
if grep -Eq '^  github\.com/mattn/go-(colorable|isatty)@' "$output_dir/THIRD_PARTY_MANIFEST.txt"; then
  echo "modules outside the shipped build graphs must not be attributed as bundled" >&2
  exit 1
fi

# A third_party tree without a single license file means the collector stopped
# matching, not that the vendored sources became unlicensed.
unlicensed_root="$test_root/unlicensed-third-party"
unlicensed_output="$unlicensed_root/bin/legal"
mkdir -p "$unlicensed_root/scripts" "$unlicensed_root/third_party/opus" "$unlicensed_root/bin"
cp "$repo_root/scripts/collect-licenses.sh" "$unlicensed_root/scripts/collect-licenses.sh"
cp "$repo_root/LICENSE" "$repo_root/NOTICE" "$unlicensed_root/"
printf 'int gul_placeholder(void) { return 0; }\n' >"$unlicensed_root/third_party/opus/opus.c"

if "$unlicensed_root/scripts/collect-licenses.sh" "$unlicensed_output" \
  >/dev/null 2>"$test_root/unlicensed.log"; then
  echo "collector accepted a third_party tree without license files" >&2
  exit 1
fi
grep -Fq 'No license files found below' "$test_root/unlicensed.log"
test ! -e "$unlicensed_output"

# A module whose directory carries no license text must stop the release, not
# ship an unattributed dependency.
stripped_go_bin="$test_root/stripped-go-bin"
stripped_module_dir="$test_root/stripped-module"
stripped_output="$test_root/stripped/legal"
mkdir -p "$stripped_go_bin" "$stripped_module_dir" "$(dirname "$stripped_output")"
touch "$stripped_module_dir/README.md"
cat >"$stripped_go_bin/go" <<'STRIPPED_GO'
#!/usr/bin/env bash

set -euo pipefail

if [[ "${1:-}" == "mod" && "${2:-}" == "download" && "$*" == *"$GUL_TEST_STRIPPED_MODULE"* ]]; then
  # Same metadata the real toolchain reports, but pointing at a directory
  # whose license file has been removed.
  "$GUL_TEST_REAL_GO" "$@" | node -e '
    const fs = require("node:fs");
    const metadata = JSON.parse(fs.readFileSync(0, "utf8"));
    metadata.Dir = process.env.GUL_TEST_STRIPPED_DIR;
    process.stdout.write(JSON.stringify(metadata));
  '
  exit 0
fi

exec "$GUL_TEST_REAL_GO" "$@"
STRIPPED_GO
chmod +x "$stripped_go_bin/go"

if GUL_TEST_STRIPPED_MODULE="github.com/adrg/xdg" \
  GUL_TEST_STRIPPED_DIR="$stripped_module_dir" \
  GUL_TEST_REAL_GO="$real_go" \
  PATH="$stripped_go_bin:$PATH" \
  "$repo_root/scripts/collect-licenses.sh" "$stripped_output" >/dev/null 2>"$test_root/stripped.log"; then
  echo "collector accepted a Go module without a license file" >&2
  exit 1
fi
grep -Fq 'No license file found for Go module: github.com/adrg/xdg' "$test_root/stripped.log"
test ! -e "$stripped_output"

# A version-specific npm attribution must not silently cover another release
# or accept a package whose inline third-party license has disappeared.
fake_node_bin="$test_root/fake-node-bin"
real_node=$(command -v node)
mkdir -p "$fake_node_bin" "$test_root/stripped-protobuf"
cat >"$fake_node_bin/node" <<'FAKE_NODE'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$#" -eq 0 ]]; then
  "$GUL_TEST_REAL_NODE" | while IFS='|' read -r name version directory; do
    if [[ "$name" == "@bufbuild/protobuf" ]]; then
      version="${GUL_TEST_PROTOBUF_VERSION:-$version}"
      directory="${GUL_TEST_PROTOBUF_DIR:-$directory}"
    fi
    printf '%s|%s|%s\n' "$name" "$version" "$directory"
  done
  exit 0
fi
exec "$GUL_TEST_REAL_NODE" "$@"
FAKE_NODE
chmod +x "$fake_node_bin/node"
if GUL_TEST_PROTOBUF_VERSION="1.10.2" GUL_TEST_REAL_NODE="$real_node" \
  PATH="$fake_node_bin:$PATH" "$repo_root/scripts/collect-licenses.sh" "$test_root/new-protobuf/legal" \
  >/dev/null 2>"$test_root/new-protobuf.log"; then
  echo "collector reused a version-specific npm license for an unaudited version" >&2
  exit 1
fi
grep -Fq 'Unsupported protobuf license attribution version: 1.10.2' "$test_root/new-protobuf.log"
test ! -e "$test_root/new-protobuf/legal"
if GUL_TEST_PROTOBUF_DIR="$test_root/stripped-protobuf" GUL_TEST_REAL_NODE="$real_node" \
  PATH="$fake_node_bin:$PATH" "$repo_root/scripts/collect-licenses.sh" "$test_root/stripped-protobuf-output/legal" \
  >/dev/null 2>"$test_root/stripped-protobuf.log"; then
  echo "collector accepted protobuf without its inline BSD attribution" >&2
  exit 1
fi
grep -Fq 'Missing inline protobuf license attribution' "$test_root/stripped-protobuf.log"
test ! -e "$test_root/stripped-protobuf-output/legal"

if find "$output_dir" -type l -print -quit | grep -q .; then
  echo "license bundle must contain regular files, not symlinks" >&2
  exit 1
fi
if find "$output_dir" -type f -name '*.go' -print -quit | grep -q .; then
  echo "licensed Go source must use .txt so go mod tidy ignores the bundle" >&2
  exit 1
fi

echo "license bundle: ok"
