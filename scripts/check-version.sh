#!/usr/bin/env bash
#
# internal/core/app.go holds the only hand-edited copy of the application
# version. Each packaging format needs a different representation of it, so
# this script derives them all from that constant and fails when a checked-in
# copy disagrees.
#
# Usage:
#   scripts/check-version.sh              verify every copy
#   scripts/check-version.sh --print deb  print one derived representation
#   scripts/check-version.sh --root DIR   verify a tree other than this one
#
# --print fields, shown for a constant of 1.4.0-rc.2:
#   app           1.4.0-rc.2   the constant; the release tag minus its "v"
#   semver        1.4.0        numeric base: config.yml, plists, NSIS
#   windows       1.4.0.0      four-part version: info.json, exe manifest
#   deb-upstream  1.4.0~rc2    nfpm upstream version
#   deb-revision  1            nfpm release field
#   deb           1.4.0~rc2-1  full Debian package version

set -euo pipefail

usage() {
  printf 'usage: check-version.sh [--root DIR] [--print FIELD]\n'
  printf 'fields: app semver windows deb-upstream deb-revision deb\n'
}

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
print_field=""

while [[ $# -gt 0 ]]; do
  case "$1" in
  --print)
    [[ $# -ge 2 ]] || { usage >&2; exit 2; }
    print_field=$2
    shift 2
    ;;
  --root)
    [[ $# -ge 2 ]] || { usage >&2; exit 2; }
    repo_root=$(cd "$2" && pwd)
    shift 2
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    printf 'unknown argument: %s\n' "$1" >&2
    usage >&2
    exit 2
    ;;
  esac
done

failures=0

report() {
  printf 'check-version: %s\n' "$1" >&2
  failures=$((failures + 1))
}

# extract <relative path> <sed script>: prints the captured value when the
# pattern matches exactly once. Callers use it inside a command substitution,
# so it cannot raise the failure count itself; on any problem it prints a
# diagnostic and returns nothing, which never equals a derived version.
extract() {
  local file="$repo_root/$1"
  local matches

  if [[ ! -f "$file" ]]; then
    printf 'check-version: missing file: %s\n' "$1" >&2
    return 0
  fi
  matches=$(sed -n "$2" "$file")
  if [[ -z "$matches" ]]; then
    printf 'check-version: no version field in %s\n' "$1" >&2
    return 0
  fi
  if [[ $(printf '%s\n' "$matches" | wc -l) -ne 1 ]]; then
    printf 'check-version: ambiguous version field in %s\n' "$1" >&2
    return 0
  fi
  printf '%s' "$matches"
}

# extract_lock_root <relative path>: the version of the root package entry in
# an npm lockfile. Dependency entries repeat the key at the same indentation,
# so the value is read only from inside the empty-name block.
extract_lock_root() {
  local file="$repo_root/$1"

  if [[ ! -f "$file" ]]; then
    printf 'check-version: missing file: %s\n' "$1" >&2
    return 0
  fi
  awk '
    /^    "": \{/ { in_root = 1; next }
    in_root && /^      "version": "/ {
      sub(/^      "version": "/, "")
      sub(/",?$/, "")
      print
      exit
    }
    in_root && /^    \}/ { exit }
  ' "$file"
}

# regex_escape <literal>: an extended regular expression matching exactly the
# literal text. Alphanumerics, "_" and "~" pass through; everything else is
# backslash-escaped. A hyphen is special only inside a bracket expression and
# passes through too, because "\-" is not portable across greps.
regex_escape() {
  printf '%s' "$1" | sed 's|[^A-Za-z0-9_~-]|\\&|g'
}

expect() { # expect <label> <expected> <actual>
  if [[ "$3" != "$2" ]]; then
    report "$1: found '$3', want '$2'"
  fi
}

app_file="internal/core/app.go"
app_version=$(extract "$app_file" 's/^const Version = "\([^"]*\)"$/\1/p')
if [[ -z "$app_version" ]]; then
  printf 'check-version: cannot read the Version constant from %s\n' "$app_file" >&2
  exit 1
fi

if [[ ! $app_version =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)(-([0-9A-Za-z]+(\.[0-9A-Za-z]+)*))?$ ]]; then
  printf 'check-version: unsupported version in %s: %s\n' "$app_file" "$app_version" >&2
  printf 'check-version: want MAJOR.MINOR.PATCH[-PRERELEASE], prerelease being dot-separated alphanumerics\n' >&2
  exit 1
fi

semver="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[3]}"
prerelease="${BASH_REMATCH[5]}"

# Native metadata formats that demand numeric components carry the base
# version only; the prerelease label lives in the constant and in the tag.
windows_version="$semver.0"

# dpkg sorts "~" before the empty string, so 0.3.0~alpha2 precedes 0.3.0. The
# dots of the prerelease are dropped to keep the shipped spelling stable
# (alpha.2 -> alpha2).
if [[ -n "$prerelease" ]]; then
  deb_upstream="$semver~${prerelease//./}"
else
  deb_upstream="$semver"
fi

nfpm_file="build/linux/nfpm/nfpm.yaml"
deb_revision=$(extract "$nfpm_file" 's/^release: "\([^"]*\)".*/\1/p')
if [[ -z "$deb_revision" ]]; then
  printf 'check-version: cannot read the release field from %s\n' "$nfpm_file" >&2
  exit 1
fi
deb_version="$deb_upstream-$deb_revision"

if [[ -n "$print_field" ]]; then
  case "$print_field" in
  app) printf '%s\n' "$app_version" ;;
  semver) printf '%s\n' "$semver" ;;
  windows) printf '%s\n' "$windows_version" ;;
  deb-upstream) printf '%s\n' "$deb_upstream" ;;
  deb-revision) printf '%s\n' "$deb_revision" ;;
  deb) printf '%s\n' "$deb_version" ;;
  *)
    printf 'unknown field: %s\n' "$print_field" >&2
    usage >&2
    exit 2
    ;;
  esac
  exit 0
fi

expect "build/config.yml info.version" "$semver" \
  "$(extract build/config.yml 's/^  version: "\([^"]*\)".*/\1/p')"

plist_version_script='/<key>CFBundleShortVersionString<\/key>/{n;s/.*<string>\(.*\)<\/string>.*/\1/p;}'
expect "build/darwin/Info.plist CFBundleShortVersionString" "$semver" \
  "$(extract build/darwin/Info.plist "$plist_version_script")"
expect "build/darwin/Info.dev.plist CFBundleShortVersionString" "$semver" \
  "$(extract build/darwin/Info.dev.plist "$plist_version_script")"

expect "build/windows/info.json fixed.file_version" "$windows_version" \
  "$(extract build/windows/info.json 's/.*"file_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
expect "build/windows/info.json info.ProductVersion" "$windows_version" \
  "$(extract build/windows/info.json 's/.*"ProductVersion"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

expect "build/windows/nsis/wails_tools.nsh INFO_PRODUCTVERSION" "$semver" \
  "$(extract build/windows/nsis/wails_tools.nsh 's/^[[:space:]]*!define INFO_PRODUCTVERSION "\([^"]*\)".*/\1/p')"

# The manifest carries a second assemblyIdentity for Common-Controls, so the
# product identifier from config.yml selects the application's own line.
product_identifier=$(extract build/config.yml 's/^  productIdentifier: "\([^"]*\)".*/\1/p')
if [[ -z "$product_identifier" ]]; then
  report "build/config.yml info.productIdentifier is missing"
else
  expect "build/windows/wails.exe.manifest assemblyIdentity" "$windows_version" \
    "$(extract build/windows/wails.exe.manifest "s/.*name=\"$product_identifier\" version=\"\([^\"]*\)\".*/\1/p")"
fi

expect "$nfpm_file version" "$deb_upstream" \
  "$(extract "$nfpm_file" 's/^version: "\([^"]*\)".*/\1/p')"

# The frontend package carries the full version, prerelease included; npm
# rewrites both lockfile copies from package.json on install.
frontend_version_script='s/^  "version": "\([^"]*\)",$/\1/p'
expect "frontend/package.json version" "$app_version" \
  "$(extract frontend/package.json "$frontend_version_script")"
expect "frontend/package-lock.json version" "$app_version" \
  "$(extract frontend/package-lock.json "$frontend_version_script")"
expect "frontend/package-lock.json root package version" "$app_version" \
  "$(extract_lock_root frontend/package-lock.json)"

# CI must derive the Debian version through this script instead of repeating
# it; a literal copy there is invisible to every check above. Both shipped
# spellings are searched, each as a whole token: the upstream version of a
# release without a prerelease is a bare semver, and a substring search would
# flag the pinned toolchain versions (WAILS3_VERSION: v3.0.0-beta.11) that
# legitimately contain one.
ci_file="$repo_root/.github/workflows/ci.yml"
if [[ ! -f "$ci_file" ]]; then
  report "missing file: .github/workflows/ci.yml"
else
  # A version token ends where a character that cannot belong to one begins.
  token_start='(^|[^0-9A-Za-z._~+-])'
  token_end='([^0-9A-Za-z._~+-]|$)'
  for hardcoded_version in "$deb_version" "$deb_upstream"; do
    if grep -Eq "$token_start$(regex_escape "$hardcoded_version")$token_end" "$ci_file"; then
      report ".github/workflows/ci.yml hardcodes '$hardcoded_version'; use scripts/check-version.sh --print deb"
      break
    fi
  done
fi

if [[ "$failures" -ne 0 ]]; then
  printf 'check-version: %d copy/copies disagree with %s (%s)\n' \
    "$failures" "$app_file" "$app_version" >&2
  exit 1
fi

printf 'check-version: %s is consistent across packaging metadata\n' "$app_version"
