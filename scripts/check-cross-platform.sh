#!/usr/bin/env bash
# Resolves the module graph for every platform we ship.
#
# The local gate builds for the host only, so a dependency that exists on one
# platform alone - the Windows toast library the Wails notification service
# pulls in, for one - is invisible until CI fails on a machine nobody has.
# `go list -deps` needs every module in go.sum for the target, which is
# exactly the failure this catches, without needing a cross compiler.
#
# Tags mirror what the release builds use, so a package excluded from a
# release build is not resolved here either.
set -euo pipefail

targets=(windows/amd64 linux/amd64 darwin/amd64 darwin/arm64)
failed=0

for target in "${targets[@]}"; do
  os="${target%%/*}"
  arch="${target##*/}"
  if ! output=$(GOOS="$os" GOARCH="$arch" go list -tags production -deps ./... 2>&1 >/dev/null); then
    echo "check-cross-platform: $target does not resolve" >&2
    printf '%s\n' "$output" >&2
    failed=1
  fi
done

if [[ "$failed" -ne 0 ]]; then
  echo "check-cross-platform: run 'go mod tidy' - it records every platform's modules" >&2
  exit 1
fi

echo "check-cross-platform: ${#targets[@]} targets resolve"
