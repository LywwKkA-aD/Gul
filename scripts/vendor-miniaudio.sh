#!/usr/bin/env bash
# Vendors miniaudio (split .c/.h pair, available since 0.11.22) into
# third_party/miniaudio and applies any patches from
# third_party/miniaudio/patches/. There are none at present: the vendored copy
# is byte-identical to upstream, which the checksums below assert. To bump:
# update VERSION and the SHA256 pins, run the script, review the diff, re-run
# the audio tests.
#
# GitHub publishes no checksums for raw files: the pins below were computed
# on first vendoring (trust-on-first-use) and guard against silent drift.
set -euo pipefail

VERSION="0.11.25"
SHA256_H="ac7af4de748b7e26b777f37e01cee313a308a7296a3eb080e2906b320cc55c89"
SHA256_C="ab1984bb9804ffd7b0303813595d0b345a8a86c34da1daffc353a14b34102a65"
SHA256_LICENSE="457f1b500e0adf6bc059edddfa78a2f62012e7c3bb43476c20e0bd23b25ba0eb"
BASE="https://raw.githubusercontent.com/mackron/miniaudio/${VERSION}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/third_party/miniaudio"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "downloading miniaudio ${VERSION}"
curl -fsSL "$BASE/miniaudio.h" -o "$TMP/miniaudio.h"
curl -fsSL "$BASE/miniaudio.c" -o "$TMP/miniaudio.c"
curl -fsSL "$BASE/LICENSE" -o "$TMP/LICENSE"
shasum -a 256 -c - <<EOF
${SHA256_H}  ${TMP}/miniaudio.h
${SHA256_C}  ${TMP}/miniaudio.c
${SHA256_LICENSE}  ${TMP}/LICENSE
EOF

echo "refreshing $VENDOR (patches/, if any, is kept)"
mkdir -p "$VENDOR"
rm -f "$VENDOR/miniaudio.h" "$VENDOR/miniaudio.c" "$VENDOR/LICENSE" "$VENDOR/VERSION"
cp "$TMP/miniaudio.h" "$TMP/miniaudio.c" "$TMP/LICENSE" "$VENDOR/"

for p in "$VENDOR"/patches/*.patch; do
  [ -e "$p" ] || continue
  echo "applying $(basename "$p")"
  patch -p1 -d "$VENDOR" --no-backup-if-mismatch <"$p"
done

cat > "$VENDOR/VERSION" <<EOF
miniaudio ${VERSION}
source: ${BASE}/{miniaudio.h,miniaudio.c,LICENSE}
sha256 (pristine, before patches):
  miniaudio.h ${SHA256_H}
  miniaudio.c ${SHA256_C}
patches: none - the vendored copy matches the checksums above exactly.
  Anything under patches/*.patch is applied on top by this script.
update: scripts/vendor-miniaudio.sh
EOF

echo "vendored miniaudio ${VERSION}"
