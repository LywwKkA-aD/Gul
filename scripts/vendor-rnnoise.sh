#!/usr/bin/env bash
# Vendors RNNoise into third_party/rnnoise, builds the binary weights blob
# embedded by internal/dsp/rnnoise and regenerates the cgo include stubs in
# that package. Run from anywhere; paths are derived from this script.
# To bump the model or the code: update the pins below, run the script,
# review the diff, re-run the rnnoise package tests.
#
# Branch main, NOT master: master is the abandoned 0.1 tree with a different
# API and a different model (docs/research/dsp-rnnoise-speexdsp.md). The
# commit hash below is the integrity pin - git content-addresses it, so no
# separate checksum is needed for the source.
#
# The model lives outside git: upstream downloads it as a 58.6 MB tarball
# holding src/rnnoise_data.h (needed unconditionally, rnn.h includes it) and
# src/rnnoise_data.c - 78 MB of generated C that is never vendored as is.
# media.xiph.org serves that tarball very slowly - tens of KB/s, i.e. up to
# an hour - so a local copy can be passed instead:
#
#   RNNOISE_MODEL_TARBALL=/path/to/rnnoise_data-<sha256>.tar.gz scripts/vendor-rnnoise.sh
#
# Either way the tarball is checked against the sha256 below, which is what
# upstream model_version holds and what download_model.sh verifies.
#
# From that model the script builds dump_weights_blob once (write_weights.c
# with -DDUMP_BINARY_WEIGHTS) and keeps only its output, weights_blob.bin.
# The library is then compiled with -DUSE_WEIGHTS_FILE so the default model
# is not linked in and the blob is the single source of weights.
#
# rnnoise_data.c cannot be dropped entirely though: init_rnnoise lives at its
# very end, outside the conditionals, and rnnoise_init calls it on every
# model. The script therefore vendors the file with every
# "#ifndef USE_WEIGHTS_FILE" region removed - exactly what the preprocessor
# would drop for our build - and then proves the reduction by preprocessing
# both versions with -DUSE_WEIGHTS_FILE and diffing the result.
set -euo pipefail

COMMIT="70f1d256acd4b34a572f999a05c87bf00b67730d" # branch main, 2025-02-22
REPO="https://github.com/xiph/rnnoise.git"
MODEL_SHA256="0a8755f8e2d834eff6a54714ecc7d75f9932e845df35f8b59bc52a7cfe6e8b37"
MODEL_URL="https://media.xiph.org/rnnoise/models/rnnoise_data-${MODEL_SHA256}.tar.gz"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/third_party/rnnoise"
PKG="$ROOT/internal/dsp/rnnoise"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

SRC="$TMP/rnnoise"
echo "fetching rnnoise ${COMMIT:0:12} from ${REPO}"
git init -q "$SRC"
git -C "$SRC" remote add origin "$REPO"
git -C "$SRC" fetch -q --depth 1 origin "$COMMIT"
git -C "$SRC" checkout -q FETCH_HEAD
got="$(git -C "$SRC" rev-parse HEAD)"
if [ "$got" != "$COMMIT" ]; then
  echo "checked out $got, expected $COMMIT" >&2
  exit 1
fi
if [ "$(cat "$SRC/model_version")" != "$MODEL_SHA256" ]; then
  echo "model_version at $COMMIT is $(cat "$SRC/model_version"), expected $MODEL_SHA256" >&2
  exit 1
fi

TARBALL="${RNNOISE_MODEL_TARBALL:-}"
if [ -n "$TARBALL" ]; then
  echo "using local model tarball $TARBALL"
else
  TARBALL="$TMP/rnnoise_data.tar.gz"
  echo "downloading model from ${MODEL_URL} (58.6 MB, very slow)"
  curl -fsSL "$MODEL_URL" -o "$TARBALL"
fi
echo "${MODEL_SHA256}  ${TARBALL}" | shasum -a 256 -c -
tar -xzmf "$TARBALL" -C "$SRC" src/rnnoise_data.c src/rnnoise_data.h

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

# Makefile.am is the authoritative list of what the library is made of.
SOURCES="$(mk_var RNNOISE_SOURCES "$SRC/Makefile.am")"
HEADERS="$(mk_var noinst_HEADERS "$SRC/Makefile.am")"
# mk_var only understands "VAR = ..."; if upstream reorganizes Makefile.am
# (+=, :=, renamed variables), fail here instead of a confusing cp error later.
if [ -z "$SOURCES" ] || [ -z "$HEADERS" ]; then
  echo "Makefile.am parse produced an empty file list; its layout changed" >&2
  exit 1
fi

echo "building dump_weights_blob"
"${CC:-cc}" -O2 -DDUMP_BINARY_WEIGHTS -I"$SRC/include" -I"$SRC/src" \
  -o "$TMP/dump_weights_blob" "$SRC/src/write_weights.c" -lm
(cd "$TMP" && ./dump_weights_blob)
blob_bytes="$(wc -c <"$TMP/weights_blob.bin" | tr -d ' ')"
blob_sha="$(shasum -a 256 "$TMP/weights_blob.bin" | cut -d' ' -f1)"

# Drop every "#ifndef USE_WEIGHTS_FILE" region, nested conditionals included.
echo "reducing src/rnnoise_data.c"
{
  cat <<'EOF'
/* Reduced by scripts/vendor-rnnoise.sh: the weight arrays of the upstream
   78 MB file are gone, only what a -DUSE_WEIGHTS_FILE build compiles is
   left. The weights now come from the embedded blob; see VERSION. */
EOF
  awk '
    /^#ifndef USE_WEIGHTS_FILE$/ && depth == 0 { depth = 1; next }
    depth > 0 {
      if ($0 ~ /^#[ \t]*(if|ifdef|ifndef)/) depth++
      else if ($0 ~ /^#[ \t]*endif/) { depth--; if (depth == 0) next }
      next
    }
    { print }
  ' "$SRC/src/rnnoise_data.c"
} >"$TMP/rnnoise_data.c"

# Both files must look the same to the compiler once USE_WEIGHTS_FILE is set.
pp() {
  "${CC:-cc}" -E -DUSE_WEIGHTS_FILE -I"$SRC/include" -I"$SRC/src" "$1" |
    grep -v '^#' | grep -v '^[[:space:]]*$'
}
if ! diff -q <(pp "$SRC/src/rnnoise_data.c") <(pp "$TMP/rnnoise_data.c") >/dev/null; then
  echo "reduced rnnoise_data.c does not preprocess like the original" >&2
  exit 1
fi

echo "refreshing $VENDOR"
rm -rf "$VENDOR"
mkdir -p "$VENDOR/include"
cp "$SRC/include/rnnoise.h" "$VENDOR/include/"
cp "$SRC/COPYING" "$SRC/AUTHORS" "$VENDOR/"
for f in $SOURCES $HEADERS; do
  mkdir -p "$VENDOR/$(dirname "$f")"
  cp "$SRC/$f" "$VENDOR/$f"
done
cp "$TMP/rnnoise_data.c" "$VENDOR/src/rnnoise_data.c"

mkdir -p "$PKG"
cp "$TMP/weights_blob.bin" "$PKG/weights_blob.bin"
rm -f "$PKG"/z_*.c
count=0
for f in $SOURCES; do
  stub="z_$(printf '%s' "$f" | tr '/' '_')"
  printf '#include "../../../third_party/rnnoise/%s"\n' "$f" >"$PKG/$stub"
  count=$((count + 1))
done

cat >"$VENDOR/VERSION" <<EOF
rnnoise ${COMMIT} (branch main, 2025-02-22)
repo: ${REPO}
subset: include/rnnoise.h COPYING AUTHORS + Makefile.am RNNOISE_SOURCES and
  noinst_HEADERS
model: ${MODEL_URL}
  sha256: ${MODEL_SHA256} (= model_version at this commit)
  taken from it: src/rnnoise_data.h and src/rnnoise_data.c reduced to what a
  -DUSE_WEIGHTS_FILE build compiles (78 MB of weight arrays dropped)
weights: internal/dsp/rnnoise/weights_blob.bin, ${blob_bytes} bytes
  sha256: ${blob_sha}
  built by dump_weights_blob (src/write_weights.c -DDUMP_BINARY_WEIGHTS);
  the library is compiled with -DUSE_WEIGHTS_FILE, so this blob is the only
  copy of the model in the binary
  it carries every array upstream ships: float32 weights for every layer
  (11509888 bytes) plus int8 copies for the seven layers that have them
  (2802112 bytes: conv2 and gru{1,2,3} input/recurrent). compute_linear
  prefers the float copy when present, so this blob runs bit-for-bit the
  model an upstream default build runs. There is NO safe reduction here:
  conv1, dense_out and vad_dense exist only as float (init_rnnoise passes
  NULL for their int8 names), and a record missing from the blob is silently
  NULL - linear_init still succeeds and compute_linear zero-fills that
  layer's output, so a stripped blob produces a garbage model (vad_prob
  included) with no error anywhere. Slimming the model means editing
  init_rnnoise - a quality change for the M3 A/B, not for this script.
x86 builds: nnet.h emits '#warning "Only SSE and SSE2 are available..."'
  once per translation unit on amd64. Deliberate: run-time SIMD dispatch
  needs RNN_ENABLE_X86_RTCD plus per-file -mavx2, which cgo cannot express.
  The warning is expected and harmless - do not "fix" it by defining that
  macro.
update: scripts/vendor-rnnoise.sh
EOF

echo "vendored rnnoise ${COMMIT:0:12}, ${count} stubs in ${PKG}, blob ${blob_bytes} bytes"
