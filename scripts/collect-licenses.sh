#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
requested_output=${1:-"$repo_root/bin/legal"}

if [[ "$requested_output" != /* ]]; then
  requested_output="$repo_root/$requested_output"
fi

output_parent=$(dirname "$requested_output")
output_name=$(basename "$requested_output")
if [[ "$output_name" != "legal" ]]; then
  echo "unsafe license output path: $requested_output" >&2
  exit 1
fi

mkdir -p "$output_parent"
output_parent=$(cd "$output_parent" && pwd -P)
output_dir="$output_parent/$output_name"
if [[ "$output_dir" == "/" || "$output_dir" == "$repo_root" || -L "$output_dir" ]]; then
  echo "unsafe license output path: $output_dir" >&2
  exit 1
fi
if [[ -e "$output_dir" && ! -f "$output_dir/THIRD_PARTY_MANIFEST.txt" ]]; then
  echo "refusing to replace an unmarked directory: $output_dir" >&2
  exit 1
fi

staging_dir=$(mktemp -d "$output_parent/.gul-legal.XXXXXX")
cleanup() {
  if [[ -n "${staging_dir:-}" && -d "$staging_dir" ]]; then
    rm -rf -- "$staging_dir"
  fi
}
trap cleanup EXIT

licenses_dir="$staging_dir/THIRD_PARTY_LICENSES"
manifest="$staging_dir/THIRD_PARTY_MANIFEST.txt"
mkdir -p "$licenses_dir/vendored" "$licenses_dir/go" "$licenses_dir/npm"

cp "$repo_root/LICENSE" "$staging_dir/LICENSE"
cp "$repo_root/NOTICE" "$staging_dir/NOTICE"
{
  cat "$repo_root/LICENSE"
  printf '\n'
  cat "$repo_root/NOTICE"
} >"$staging_dir/copyright"

{
  echo "Gul third-party license manifest"
  echo
  echo "Vendored native sources"
} >"$manifest"

while IFS= read -r source_file; do
  relative_path=${source_file#"$repo_root/"}
  destination="$licenses_dir/vendored/$relative_path"
  mkdir -p "$(dirname "$destination")"
  cp "$source_file" "$destination"
  printf '  %s\n' "$relative_path" >>"$manifest"
done < <(
  find "$repo_root/third_party" -type f \
    \( -iname '*license*' -o -iname '*copying*' -o -iname 'notice*' \
       -o -iname 'patents*' -o -iname 'authors*' -o -iname 'version' \) \
    -print | sort
)

{
  echo
  echo "Go toolchain and modules"
} >>"$manifest"

go_root=$(go env GOROOT)
go_license=""
for candidate in "$go_root/LICENSE" "$(dirname "$go_root")/LICENSE"; do
  if [[ -f "$candidate" ]]; then
    go_license="$candidate"
    break
  fi
done
if [[ -z "$go_license" ]]; then
  echo "Go toolchain license not found below $go_root" >&2
  exit 1
fi
mkdir -p "$licenses_dir/go/toolchain"
cp "$go_license" "$licenses_dir/go/toolchain/LICENSE"
if [[ -f "$go_root/PATENTS" ]]; then
  cp "$go_root/PATENTS" "$licenses_dir/go/toolchain/PATENTS"
fi
printf '  Go toolchain: %s\n' "$(go version)" >>"$manifest"

# This union is the platform-specific runtime graph across macOS, Windows and
# Linux. Test-only modules and build tools are deliberately excluded.
go_modules=(
  github.com/LywwKkA-aD/gumble
  github.com/adrg/xdg
  github.com/coder/websocket
  github.com/godbus/dbus/v5
  github.com/go-ole/go-ole
  github.com/mattn/go-colorable
  github.com/mattn/go-isatty
  github.com/wailsapp/wails/v3
  golang.org/x/net
  golang.org/x/sys
  google.golang.org/protobuf
)

for module_path in "${go_modules[@]}"; do
  # `go list -m` can report an empty .Dir for a selected module that is only
  # used on another platform. Download the exact selected version instead;
  # the Go command verifies its module and go.mod checksums against go.sum.
  module_info=$(
    cd "$repo_root"
    go mod download -json "$module_path" |
      GUL_LICENSE_MODULE_PATH="$module_path" node -e '
        const fs = require("node:fs");
        const metadata = JSON.parse(fs.readFileSync(0, "utf8"));
        const expectedPath = process.env.GUL_LICENSE_MODULE_PATH;

        if (metadata.Error) {
          throw new Error(`failed to download ${expectedPath}: ${metadata.Error}`);
        }
        if (metadata.Path !== expectedPath) {
          throw new Error(`downloaded unexpected Go module: ${metadata.Path}`);
        }
        if (!metadata.Version || !metadata.Dir) {
          throw new Error(`download metadata is incomplete for ${expectedPath}`);
        }
        if (!metadata.Sum || !metadata.GoModSum) {
          throw new Error(`download checksums are missing for ${expectedPath}`);
        }

        process.stdout.write(`${metadata.Version}|${metadata.Dir}`);
      '
  )
  IFS='|' read -r module_version module_dir <<<"$module_info"
  if [[ -z "$module_dir" || ! -d "$module_dir" ]]; then
    echo "Go module is not downloaded: $module_path@$module_version" >&2
    exit 1
  fi

  module_destination="$licenses_dir/go/$module_path"
  mkdir -p "$module_destination"
  found_license=0
  while IFS= read -r source_file; do
    module_relative_path=${source_file#"$module_dir/"}
    destination="$module_destination/$module_relative_path"
    mkdir -p "$(dirname "$destination")"
    cp "$source_file" "$destination"
    found_license=1
  done < <(
    find "$module_dir" -type f \
      \( -iname '*license*' -o -iname '*copying*' -o -iname 'notice*' -o -iname 'patents*' \) \
      -not -path "$module_dir/examples/*" \
      -not -path "$module_dir/internal/templates/*" \
      -not -path "$module_dir/internal/setupwizard/*" \
      -print | sort
  )

  if [[ "$found_license" -eq 0 ]]; then
    echo "No license file found for Go module: $module_path@$module_version" >&2
    exit 1
  fi
  printf '  %s@%s\n' "$module_path" "${module_version:-devel}" >>"$manifest"

  if [[ "$module_path" == "github.com/wailsapp/wails/v3" ]]; then
    # These runtime files carry inline licenses or derivation notices rather
    # than a neighbouring LICENSE file. Preserve the complete attributed
    # source so minification/linking cannot discard the notices.
    mkdir -p \
      "$module_destination/internal/assetserver" \
      "$module_destination/pkg/w32" \
      "$module_destination/pkg/application"
    cp "$module_dir/internal/assetserver/ringqueue.go" \
      "$module_destination/internal/assetserver/ringqueue-LICENSE-and-source.txt"
    cp "$module_dir/pkg/w32/clipboard.go" \
      "$module_destination/pkg/w32/clipboard-LICENSE-and-source.txt"
    cp "$module_dir/pkg/application/systemtray_linux.go" \
      "$module_destination/pkg/application/fyne-io-systray-NOTICE-and-source.txt"
    cp "$module_dir/pkg/application/screenmanager.go" \
      "$module_destination/pkg/application/Chromium-NOTICE-and-source.txt"
    # Wails' Linux tray implementation is derived from fyne-io/systray,
    # licensed Apache-2.0. The canonical Apache text is already vendored for
    # Abseil, so reuse it under an explicit attribution filename.
    cp "$repo_root/third_party/webrtc-apm/absl-LICENSE" \
      "$module_destination/pkg/application/fyne-io-systray-LICENSE"
  fi
done

{
  echo
  echo "Bundled frontend packages"
} >>"$manifest"

while IFS='|' read -r package_name package_version package_dir; do
  [[ -n "$package_name" ]] || continue
  printf '  %s@%s\n' "$package_name" "$package_version" >>"$manifest"

  # The npm runtime package is covered by the Wails MIT license copied above,
  # but it also embeds three attributed snippets whose comments are removed by
  # our production minifier. Preserve their complete licensed source here.
  if [[ "$package_name" == "@wailsio/runtime" ]]; then
    package_destination="$licenses_dir/npm/$package_name"
    mkdir -p "$package_destination"
    cp "$package_dir/dist/nanoid.js" "$package_destination/NanoID-LICENSE-and-source.js"
    cp "$package_dir/dist/callable.js" "$package_destination/is-callable-LICENSE-and-source.js"
    cp "$package_dir/dist/utils.js" "$package_destination/HTMX-LICENSE-and-source.js"
    continue
  fi

  package_destination="$licenses_dir/npm/$package_name"
  mkdir -p "$package_destination"
  found_license=0
  while IFS= read -r source_file; do
    cp "$source_file" "$package_destination/$(basename "$source_file")"
    found_license=1
  done < <(
    find "$package_dir" -maxdepth 1 -type f \
      \( -iname '*license*' -o -iname '*copying*' -o -iname 'notice*' \) \
      -print | sort
  )

  if [[ "$found_license" -eq 0 ]]; then
    echo "No license file found for npm package: $package_name@$package_version" >&2
    exit 1
  fi
done < <(
  cd "$repo_root"
  node <<'NODE'
const path = require("node:path");
const lock = require("./frontend/package-lock.json");

for (const [packagePath, metadata] of Object.entries(lock.packages ?? {})) {
  if (!packagePath.startsWith("node_modules/") || metadata.dev) {
    continue;
  }
  const name = metadata.name ?? packagePath.slice("node_modules/".length);
  const directory = path.resolve("frontend", packagePath);
  process.stdout.write(`${name}|${metadata.version ?? "unknown"}|${directory}\n`);
}
NODE
)

# Vite injects its modulepreload runtime into the production bundle. Vite is a
# devDependency, so it is intentionally absent from the loop above; its
# aggregate license file covers the injected runtime and bundled helpers.
vite_dir="$repo_root/frontend/node_modules/vite"
if [[ ! -f "$vite_dir/LICENSE.md" ]]; then
  echo "Vite license file not found" >&2
  exit 1
fi
mkdir -p "$licenses_dir/npm/vite"
cp "$vite_dir/LICENSE.md" "$licenses_dir/npm/vite/LICENSE.md"
printf '  vite@%s (injected modulepreload runtime)\n' \
  "$(node -p 'require(process.argv[1]).version' "$vite_dir/package.json")" >>"$manifest"

chmod -R u=rwX,go=rX "$staging_dir"
if [[ -e "$output_dir" ]]; then
  rm -rf -- "$output_dir"
fi
mv "$staging_dir" "$output_dir"
staging_dir=""

echo "Collected release licenses in $output_dir"
