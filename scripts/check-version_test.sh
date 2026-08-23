#!/usr/bin/env bash
#
# Exercises scripts/check-version.sh against the repository and against a
# synthetic tree whose version differs from the shipped one, so the checker is
# proven to derive representations rather than to recognise today's constant.

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
checker="$repo_root/scripts/check-version.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/gul-version-test.XXXXXX")
trap 'rm -rf -- "$test_root"' EXIT

bash "$checker" >/dev/null

repo_deb=$(bash "$checker" --print deb)
repo_upstream=$(bash "$checker" --print deb-upstream)
repo_revision=$(bash "$checker" --print deb-revision)
test "$repo_deb" = "$repo_upstream-$repo_revision"

pristine="$test_root/pristine"
mkdir -p \
  "$pristine/internal/core" \
  "$pristine/build/darwin" \
  "$pristine/build/windows/nsis" \
  "$pristine/build/linux/nfpm" \
  "$pristine/frontend" \
  "$pristine/.github/workflows"

cat >"$pristine/internal/core/app.go" <<'APP_GO'
package core

const Version = "1.4.0-rc.2"
APP_GO

cat >"$pristine/build/config.yml" <<'CONFIG_YML'
version: '3'

info:
  productIdentifier: "com.example.app"
  version: "1.4.0" # Numeric platform version
CONFIG_YML

cat >"$pristine/build/darwin/Info.plist" <<'INFO_PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
	<dict>
		<key>CFBundleShortVersionString</key>
		<string>1.4.0</string>
		<key>CFBundleVersion</key>
		<string>1</string>
	</dict>
</plist>
INFO_PLIST
cp "$pristine/build/darwin/Info.plist" "$pristine/build/darwin/Info.dev.plist"

cat >"$pristine/build/windows/info.json" <<'INFO_JSON'
{
	"fixed": {
		"file_version": "1.4.0.0"
	},
	"info": {
		"0000": {
			"ProductVersion": "1.4.0.0"
		}
	}
}
INFO_JSON

cat >"$pristine/build/windows/nsis/wails_tools.nsh" <<'WAILS_TOOLS'
!ifndef INFO_PRODUCTVERSION
    !define INFO_PRODUCTVERSION "1.4.0"
!endif
WAILS_TOOLS

cat >"$pristine/build/windows/wails.exe.manifest" <<'MANIFEST'
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly manifestVersion="1.0">
    <assemblyIdentity type="win32" name="com.example.app" version="1.4.0.0" processorArchitecture="*"/>
    <dependency>
        <dependentAssembly>
            <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*"/>
        </dependentAssembly>
    </dependency>
</assembly>
MANIFEST

cat >"$pristine/build/linux/nfpm/nfpm.yaml" <<'NFPM_YAML'
name: "gul"
version: "1.4.0~rc2"
release: "1"
NFPM_YAML

cat >"$pristine/frontend/package.json" <<'PACKAGE_JSON'
{
  "name": "example-frontend",
  "private": true,
  "version": "1.4.0-rc.2",
  "dependencies": {
    "react": "19.2.8"
  }
}
PACKAGE_JSON

# The dependency entry repeats the key at the indentation of the root entry,
# which is what the root-package extraction has to survive.
cat >"$pristine/frontend/package-lock.json" <<'PACKAGE_LOCK'
{
  "name": "example-frontend",
  "version": "1.4.0-rc.2",
  "lockfileVersion": 3,
  "packages": {
    "": {
      "name": "example-frontend",
      "version": "1.4.0-rc.2",
      "dependencies": {
        "react": "19.2.8"
      }
    },
    "node_modules/react": {
      "version": "19.2.8"
    }
  }
}
PACKAGE_LOCK

cat >"$pristine/.github/workflows/ci.yml" <<'CI_YML'
name: CI
CI_YML

bash "$checker" --root "$pristine" >/dev/null
test "$(bash "$checker" --root "$pristine" --print app)" = "1.4.0-rc.2"
test "$(bash "$checker" --root "$pristine" --print semver)" = "1.4.0"
test "$(bash "$checker" --root "$pristine" --print windows)" = "1.4.0.0"
test "$(bash "$checker" --root "$pristine" --print deb)" = "1.4.0~rc2-1"

case_root="$test_root/case"

setup_case() {
  rm -rf -- "$case_root"
  mkdir -p "$case_root"
  cp -R "$pristine/." "$case_root/"
}

reject() { # reject <label> <expected diagnostic fragment>
  local output
  if output=$(bash "$checker" --root "$case_root" 2>&1); then
    printf 'check-version accepted %s\n' "$1" >&2
    exit 1
  fi
  if ! grep -Fq "$2" <<<"$output"; then
    printf 'check-version rejected %s without naming it:\n%s\n' "$1" "$output" >&2
    exit 1
  fi
}

setup_case
sed -i.bak 's/  version: "1.4.0"/  version: "1.4.1"/' "$case_root/build/config.yml"
reject "a stale build/config.yml version" "build/config.yml info.version"

setup_case
sed -i.bak 's|<string>1.4.0</string>|<string>1.4.0.0</string>|' "$case_root/build/darwin/Info.plist"
reject "a stale Info.plist version" "build/darwin/Info.plist"

setup_case
sed -i.bak 's|<string>1.4.0</string>|<string>1.3.0</string>|' "$case_root/build/darwin/Info.dev.plist"
reject "a stale Info.dev.plist version" "build/darwin/Info.dev.plist"

setup_case
sed -i.bak 's/"ProductVersion": "1.4.0.0"/"ProductVersion": "1.4.0.1"/' "$case_root/build/windows/info.json"
reject "a stale info.json ProductVersion" "info.ProductVersion"

setup_case
sed -i.bak 's/"file_version": "1.4.0.0"/"file_version": "1.4.0.1"/' "$case_root/build/windows/info.json"
reject "a stale info.json file_version" "fixed.file_version"

setup_case
sed -i.bak 's/INFO_PRODUCTVERSION "1.4.0"/INFO_PRODUCTVERSION "0.9.9"/' "$case_root/build/windows/nsis/wails_tools.nsh"
reject "a stale NSIS product version" "INFO_PRODUCTVERSION"

setup_case
sed -i.bak 's/name="com.example.app" version="1.4.0.0"/name="com.example.app" version="1.4.0.1"/' \
  "$case_root/build/windows/wails.exe.manifest"
reject "a stale manifest version" "wails.exe.manifest"

setup_case
sed -i.bak 's/version: "1.4.0~rc2"/version: "1.4.0~rc1"/' "$case_root/build/linux/nfpm/nfpm.yaml"
reject "a stale nfpm version" "nfpm.yaml version"

setup_case
printf '          test "$(dpkg-deb --field bin/gul.deb Version)" = "1.4.0~rc2-1"\n' \
  >>"$case_root/.github/workflows/ci.yml"
reject "a hardcoded Debian version in CI" "hardcodes"

setup_case
sed -i.bak 's/^  "version": "1.4.0-rc.2",$/  "version": "1.4.0-rc.3",/' \
  "$case_root/frontend/package.json"
reject "a stale frontend package version" "frontend/package.json version"

setup_case
sed -i.bak 's/^  "version": "1.4.0-rc.2",$/  "version": "1.4.0-rc.3",/' \
  "$case_root/frontend/package-lock.json"
reject "a stale lockfile version" "frontend/package-lock.json version"

setup_case
sed -i.bak 's/^      "version": "1.4.0-rc.2",$/      "version": "1.4.0-rc.3",/' \
  "$case_root/frontend/package-lock.json"
reject "a stale lockfile root package version" "root package version"

setup_case
sed -i.bak 's/^  version: "1.4.0" # Numeric platform version/  version: "1.4.0"\
  version: "1.4.0"/' "$case_root/build/config.yml"
reject "an ambiguous version field" "ambiguous version field"

setup_case
rm "$case_root/build/darwin/Info.plist"
reject "a missing metadata file" "missing file"

# The Common-Controls dependency carries its own version; it must not be
# mistaken for the application's.
setup_case
sed -i.bak 's/name="Microsoft.Windows.Common-Controls" version="6.0.0.0"/name="Microsoft.Windows.Common-Controls" version="6.0.0.1"/' \
  "$case_root/build/windows/wails.exe.manifest"
bash "$checker" --root "$case_root" >/dev/null

setup_case
sed -i.bak 's/const Version = "1.4.0-rc.2"/const Version = "1.4"/' "$case_root/internal/core/app.go"
reject "an unparsable version constant" "unsupported version"

setup_case
sed -i.bak 's/const Version = "1.4.0-rc.2"/const APIVersion = "1.4.0"/' "$case_root/internal/core/app.go"
reject "a missing version constant" "cannot read the Version constant"

echo "version metadata: ok"
