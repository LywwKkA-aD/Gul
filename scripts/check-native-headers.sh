#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cxx=${CXX:-$(go env CXX)}

check_header() {
  local header=$1
  local expression=$2

  printf '%s\n' \
    "#include \"$header\"" \
    'int main() {' \
    "  return $expression;" \
    '}' |
    "$cxx" \
      -std=c++17 \
      -fsyntax-only \
      -I"$repo_root/third_party/webrtc-apm/webrtc" \
      -I"$repo_root/third_party/webrtc-apm/abseil" \
      -x c++ -
}

check_header \
  'rtc_base/trace_event.h' \
  'static_cast<int>(TRACE_EVENT_SCOPE_GLOBAL)'
check_header \
  'modules/audio_processing/aec3/multi_channel_content_detector.h' \
  'sizeof(webrtc::MultiChannelContentDetector) == 0'

echo "native WebRTC headers: ok"
