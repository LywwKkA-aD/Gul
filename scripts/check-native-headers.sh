#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cxx=${CXX:-$(go env CXX)}

printf '%s\n' \
  '#include "rtc_base/trace_event.h"' \
  'int main() {' \
  '  return static_cast<int>(TRACE_EVENT_SCOPE_GLOBAL);' \
  '}' |
  "$cxx" \
    -std=c++17 \
    -fsyntax-only \
    -I"$repo_root/third_party/webrtc-apm/webrtc" \
    -I"$repo_root/third_party/webrtc-apm/abseil" \
    -x c++ -

echo "native WebRTC headers: ok"
