#!/usr/bin/env bash
# Vendors libopus into third_party/opus and regenerates the cgo include stubs
# in internal/dsp/opus. Run from anywhere; paths are derived from this script.
# To bump the codec: update VERSION and SHA256 below, run the script, review
# the diff, run the opus package tests.
#
# The vendored subset is the float build without ML features (no dnn/), no
# intrinsics and no RTCD; see docs/research/alternatives/opus.md for the
# measurements behind that choice.
set -euo pipefail

VERSION="1.6.1"
SHA256="6ffcb593207be92584df15b32466ed64bbec99109f007c82205f0194572411a1"
URL="https://downloads.xiph.org/releases/opus/opus-${VERSION}.tar.gz"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/third_party/opus"
PKG="$ROOT/internal/dsp/opus"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "downloading opus ${VERSION}"
curl -fsSL "$URL" -o "$TMP/opus.tar.gz"
echo "${SHA256}  ${TMP}/opus.tar.gz" | shasum -a 256 -c -
tar -xzf "$TMP/opus.tar.gz" -C "$TMP"
SRC="$TMP/opus-$VERSION"

echo "refreshing $VENDOR"
rm -rf "$VENDOR"
mkdir -p "$VENDOR"
cp -R "$SRC/include" "$SRC/celt" "$SRC/silk" "$SRC/src" "$VENDOR/"
cp "$SRC/COPYING" "$VENDOR/"
# The .mk files are the authoritative source lists the stubs are derived from;
# kept for traceability of what exactly is compiled.
cp "$SRC/opus_sources.mk" "$SRC/celt_sources.mk" "$SRC/silk_sources.mk" "$VENDOR/"
cat > "$VENDOR/VERSION" <<EOF
opus ${VERSION}
tarball: ${URL}
sha256: ${SHA256}
subset: include/ celt/ silk/ src/ COPYING *.mk (float build, no dnn/)
update: scripts/vendor-opus.sh
EOF

# Print the file list of one make variable ("VAR = a.c \\\n b.c ...").
mk_var() {
  awk -v var="$1" '
    $0 ~ "^" var "[ \t]*=" { grab = 1 }
    grab {
      line = $0
      sub(/^[A-Za-z0-9_]+[ \t]*=[ \t]*/, "", line)
      cont = (line ~ /\\[ \t]*$/)
      gsub(/\\/, "", line)
      n = split(line, parts, /[ \t]+/)
      for (i = 1; i <= n; i++) if (parts[i] != "") print parts[i]
      if (!cont) grab = 0
    }
  ' "$2"
}

SOURCES="$(
  mk_var OPUS_SOURCES "$SRC/opus_sources.mk"
  mk_var OPUS_SOURCES_FLOAT "$SRC/opus_sources.mk"
  mk_var CELT_SOURCES "$SRC/celt_sources.mk"
  mk_var SILK_SOURCES "$SRC/silk_sources.mk"
  mk_var SILK_SOURCES_FLOAT "$SRC/silk_sources.mk"
)"

mkdir -p "$PKG"
rm -f "$PKG"/z_*.c
count=0
for f in $SOURCES; do
  stub="z_$(printf '%s' "$f" | tr '/' '_')"
  printf '#include "../../../third_party/opus/%s"\n' "$f" > "$PKG/$stub"
  count=$((count + 1))
done

echo "vendored opus ${VERSION}, generated ${count} stubs in ${PKG}"
