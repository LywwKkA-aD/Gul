#!/usr/bin/env bash
# Vendors webrtc-audio-processing (AEC3 + HPF + NS + AGC2) together with the
# abseil-cpp slice it needs into third_party/webrtc-apm, applies our patches
# from third_party/webrtc-apm/patches/, and regenerates the cgo include stubs
# in internal/dsp/apm. Run from anywhere; paths are derived from this script.
# To bump: update the pins below, run the script, review the diff, re-run the
# apm package tests.
#
# GitLab regenerates archive tarballs on request rather than publishing signed
# release artifacts, so WAP_SHA256 is a trust-on-first-use pin (recorded when
# this script was written) guarding against silent drift, not an upstream
# checksum. ABSL_SHA256 is the checksum abseil publishes for the tag.
#
# What is deliberately left out of the vendored tree: MIPS and 32-bit ARM
# assembly (we ship amd64/arm64 desktops only), the AVX2 translation units
# (they need per-file -mavx2, which cgo cannot express; see internal/dsp/apm)
# and the upstream test helpers.
set -euo pipefail

WAP_VERSION="2.1"
WAP_SHA256="2358883a8551711f3b6169f6799c003dc1a6efd37bdb24a135a015cc9e3d7713"
WAP_URL="https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/archive/v${WAP_VERSION}/webrtc-audio-processing-v${WAP_VERSION}.tar.gz"

ABSL_VERSION="20240722.0"
ABSL_SHA256="f50e5ac311a81382da7fa75b97310e4b9006474f9560ac46f54a9967f07d4ae3"
ABSL_URL="https://github.com/abseil/abseil-cpp/archive/refs/tags/${ABSL_VERSION}.tar.gz"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VENDOR="$ROOT/third_party/webrtc-apm"
PKG="$ROOT/internal/dsp/apm"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

export LC_ALL=C

echo "downloading webrtc-audio-processing ${WAP_VERSION}"
curl -fsSL "$WAP_URL" -o "$TMP/wap.tar.gz"
echo "downloading abseil-cpp ${ABSL_VERSION}"
curl -fsSL "$ABSL_URL" -o "$TMP/absl.tar.gz"
shasum -a 256 -c - <<EOF
${WAP_SHA256}  ${TMP}/wap.tar.gz
${ABSL_SHA256}  ${TMP}/absl.tar.gz
EOF

tar -xzf "$TMP/wap.tar.gz" -C "$TMP"
tar -xzf "$TMP/absl.tar.gz" -C "$TMP"
WAP_SRC="$TMP/webrtc-audio-processing-v${WAP_VERSION}"
ABSL_SRC="$TMP/abseil-cpp-${ABSL_VERSION}"

echo "refreshing $VENDOR (patches/ is kept)"
rm -rf "$VENDOR/webrtc" "$VENDOR/abseil"
rm -f "$VENDOR/COPYING" "$VENDOR/PATENTS" "$VENDOR/absl-LICENSE" \
      "$VENDOR/meson.build" "$VENDOR/VERSION"
mkdir -p "$VENDOR"

cp "$WAP_SRC/COPYING" "$VENDOR/COPYING"
cp "$WAP_SRC/webrtc/PATENTS" "$VENDOR/PATENTS"
cp "$ABSL_SRC/LICENSE" "$VENDOR/absl-LICENSE"
# The meson files are the authoritative source lists the stubs are derived
# from; the top-level one also records the upstream compile flags.
cp "$WAP_SRC/meson.build" "$VENDOR/meson.build"
cp -R "$WAP_SRC/webrtc" "$VENDOR/webrtc"

find "$VENDOR/webrtc" -type f \( \
  -name '*.S' -o -name '*mips*' -o -name '*_avx2.cc' -o \
  -name 'test_utils.h' -o -name 'test_utils.cc' -o \
  -name '*.gn' -o -name '*.gni' -o -name '*.py' -o -name '*.proto' -o \
  -name '*.diff' \) -delete

for p in "$VENDOR"/patches/*.patch; do
  [ -e "$p" ] || continue
  echo "applying $(basename "$p")"
  patch -p1 -d "$VENDOR" --no-backup-if-mismatch <"$p"
done

# --- abseil slice -----------------------------------------------------------
# The needed subtree is the transitive closure of the absl headers the vendored
# webrtc tree (and our shim) includes, plus the implementation file that sits
# next to each of those headers. Upstream absl always spells its own includes
# as "absl/...", so a textual worklist over that pattern is exact.
echo "resolving the abseil closure"
: >"$TMP/absl-todo"
: >"$TMP/absl-done"
{
  grep -rho '#include "absl/[^"]*"' "$VENDOR/webrtc" || true
  grep -rho '#include "absl/[^"]*"' "$PKG" 2>/dev/null || true
} | sed 's/.*"\(absl\/[^"]*\)".*/\1/' | sort -u >"$TMP/absl-todo"

while [ -s "$TMP/absl-todo" ]; do
  rel="$(head -n 1 "$TMP/absl-todo")"
  tail -n +2 "$TMP/absl-todo" >"$TMP/absl-todo.next"
  mv "$TMP/absl-todo.next" "$TMP/absl-todo"
  if grep -qxF "$rel" "$TMP/absl-done"; then
    continue
  fi
  printf '%s\n' "$rel" >>"$TMP/absl-done"

  case "$rel" in
  *_test.h | *_test.cc | *_testing.h | *_testing.cc | *_benchmark.cc | *test_util*) continue ;;
  esac
  src="$ABSL_SRC/$rel"
  [ -f "$src" ] || continue

  mkdir -p "$VENDOR/abseil/$(dirname "$rel")"
  cp "$src" "$VENDOR/abseil/$rel"
  grep -o '#include "absl/[^"]*"' "$src" |
    sed 's/.*"\(absl\/[^"]*\)".*/\1/' >>"$TMP/absl-todo" || true
  case "$rel" in
  *.h) printf '%s\n' "${rel%.h}.cc" >>"$TMP/absl-todo" ;;
  esac
done

ABSL_SOURCES="$(cd "$VENDOR/abseil" && find absl -type f -name '*.cc' | sort)"

# --- webrtc source list -----------------------------------------------------
# Print the quoted entries of a meson list ("name = [ 'a.cc', ... ]", on one
# line or many), first assignment only; the conditional "+=" blocks are spelled
# out below. \047 is the single quote that delimits meson strings.
meson_list() {
  awk -v var="$1" '
    !grab && $0 ~ "^" var "[ \t]*=[ \t]*\\[" {
      grab = 1
      buf = $0
      sub(/^[^[]*\[/, "", buf)
    }
    grab {
      if (buf == "") buf = $0
      if (index(buf, "]") > 0) {
        sub(/\].*/, "", buf)
        done = 1
      }
      n = split(buf, parts, /\047/)
      for (i = 2; i <= n; i += 2) {
        if (parts[i] != "") print parts[i]
      }
      buf = ""
      if (done) exit
    }
  ' "$2"
}

# Prefix each entry of a meson list with the directory the list lives in.
module_list() {
  meson_list "$2" "$WAP_SRC/webrtc/$1/meson.build" | sed "s|^|$1/|"
}

SOURCES="$(
  module_list api api_sources
  module_list rtc_base base_sources
  module_list system_wrappers system_wrappers_sources
  module_list common_audio common_audio_sources
  module_list third_party/pffft pffft_sources
  module_list third_party/rnnoise rnnoise_sources
  module_list modules/third_party/fft fft_sources
  module_list modules/audio_coding isac_vad_sources
  module_list modules/audio_processing webrtc_audio_processing_sources
  # meson: audio_processing "if have_mips ... else" branch.
  echo modules/audio_processing/aecm/aecm_core_c.cc
)"

# meson: common_audio "if have_x86" static_library('common_audio_sse2'). SSE2 is
# baseline in the amd64 ABI, so these need no extra compiler flag from us.
SOURCES_AMD64="common_audio/fir_filter_sse.cc
common_audio/resampler/sinc_resampler_sse.cc
common_audio/third_party/ooura/fft_size_128/ooura_fft_sse2.cc"

# meson: the "if neon_opt.enabled()" blocks of common_audio and audio_processing.
SOURCES_ARM64="common_audio/fir_filter_neon.cc
common_audio/resampler/sinc_resampler_neon.cc
common_audio/signal_processing/cross_correlation_neon.c
common_audio/signal_processing/downsample_fast_neon.c
common_audio/signal_processing/min_max_operations_neon.c
common_audio/third_party/ooura/fft_size_128/ooura_fft_neon.cc
modules/audio_processing/aecm/aecm_core_neon.cc"

# --- cgo stubs --------------------------------------------------------------
# cgo only compiles files that live in the package directory, so each vendored
# translation unit gets a one-line stub next to the Go code. Stubs for
# architecture-specific sources carry a GOARCH filename suffix so the go tool
# gates them for us.
mkdir -p "$PKG"
rm -f "$PKG"/z_*

# GOOS/GOARCH tokens the go tool would read as a build constraint if a
# generated stub name happened to end in one.
GO_TOKENS="aix android darwin dragonfly freebsd hurd illumos ios js linux nacl \
netbsd openbsd plan9 solaris wasip1 windows zos 386 amd64 arm arm64 loong64 \
mips mipsle mips64 mips64le ppc64 ppc64le riscv riscv64 s390 s390x sparc \
sparc64 wasm"

# emit <path below the vendor subdir> <vendor subdir> [goarch suffix]
emit_stub() {
  case "$1" in
  *.c | *.cc) ;;
  *)
    echo "not a source path: $1 (meson list parsing went wrong)" >&2
    exit 1
    ;;
  esac
  if [ ! -f "$VENDOR/$2$1" ]; then
    echo "missing vendored source: $2$1" >&2
    exit 1
  fi
  base="z_$(printf '%s' "$1" | tr '/' '_')"
  stem="${base%.*}"
  ext="${base##*.}"
  if [ -n "${3:-}" ]; then
    stem="${stem}_$3"
  else
    for tok in $GO_TOKENS; do
      case "$stem" in
      *_"$tok")
        echo "stub name $stem.$ext ends in the build tag $tok" >&2
        exit 1
        ;;
      esac
    done
  fi
  # Flattening '/' to '_' can collide (a/b_c.cc vs a_b/c.cc): a silent
  # overwrite would drop a translation unit and only surface at link time.
  if [ -e "$PKG/$stem.$ext" ]; then
    echo "stub name collision: $stem.$ext already generated" >&2
    exit 1
  fi
  printf '#include "../../../third_party/webrtc-apm/%s%s"\n' "$2" "$1" \
    >"$PKG/$stem.$ext"
}

count=0
for f in $SOURCES; do
  emit_stub "$f" "webrtc/"
  count=$((count + 1))
done
for f in $SOURCES_AMD64; do
  emit_stub "$f" "webrtc/" amd64
  count=$((count + 1))
done
for f in $SOURCES_ARM64; do
  emit_stub "$f" "webrtc/" arm64
  count=$((count + 1))
done
for f in $ABSL_SOURCES; do
  emit_stub "$f" "abseil/"
  count=$((count + 1))
done

cat >"$VENDOR/VERSION" <<EOF
webrtc-audio-processing ${WAP_VERSION} (WebRTC M131, AEC3 + HPF + NS + AGC2)
tarball: ${WAP_URL}
sha256: ${WAP_SHA256}  (trust-on-first-use: GitLab regenerates archives)

abseil-cpp ${ABSL_VERSION}
tarball: ${ABSL_URL}
sha256: ${ABSL_SHA256}

subset:
  webrtc/        full upstream tree minus *.S, *mips*, *_avx2.cc, test helpers
  abseil/absl/   transitive closure of the absl headers webrtc includes
  meson.build and webrtc/**/meson.build are kept: they are the authoritative
  source lists the cgo stubs are generated from

include roots for the compiler: webrtc/ and abseil/. absl sits one level down
rather than at the root because the root holds a file named VERSION, and on a
case-insensitive filesystem that shadows the C++ standard header <version>.
licenses: COPYING (BSD-3-Clause), PATENTS (WebRTC grant), absl-LICENSE
  (Apache-2.0), plus the per-directory notices inside webrtc/
patches: patches/*.patch (applied on top by this script)
update: scripts/vendor-webrtc-apm.sh
EOF

echo "vendored webrtc-audio-processing ${WAP_VERSION} + abseil-cpp ${ABSL_VERSION}"
echo "generated ${count} stubs in ${PKG}"
