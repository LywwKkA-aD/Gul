#!/bin/sh
set -eu
# The caller always creates a private D-Bus, XDG tree and encrypted fixture.
export GNOME_KEYRING_CONTROL="$XDG_RUNTIME_DIR/keyring"
mkdir -p "$GNOME_KEYRING_CONTROL"
chmod 700 "$GNOME_KEYRING_CONTROL"
if test "$3" = missing; then
  gnome-keyring-daemon --start --components=secrets --control-directory="$GNOME_KEYRING_CONTROL" >/dev/null 2>&1
else
  printf '%s' "$GUL_VAULT_FIXTURE_KEYRING_PASSWORD" | gnome-keyring-daemon --unlock --components=secrets --control-directory="$GNOME_KEYRING_CONTROL" >/dev/null 2>&1
fi
run() {
  # Deliberately no password-store flag: test ordinary desktop autodetection.
  "$1" --no-sandbox --disable-gpu --user-data-dir="$GUL_VAULT_FIXTURE_PROFILE" "$2" "--gul-vault-phase=$3"
}
if test "$3" = write; then
  run "$1" "$2" write
  gdbus call --session --dest org.freedesktop.secrets --object-path /org/freedesktop/secrets --method org.freedesktop.Secret.Service.Lock "['/org/freedesktop/secrets/collection/login']" >/dev/null
  run "$1" "$2" locked
  (
    count=0
    while test "$count" -lt 80; do
      if test -f "$GUL_VAULT_FIXTURE_FILE.await-unlock"; then
        python3 "$(dirname "$0")/password-store-fixture-unlock.py"
        exit 0
      fi
      for id in p1 p2 p3 p4 p5; do
        gdbus call --session --dest org.freedesktop.secrets --object-path "/org/freedesktop/secrets/prompt/$id" --method org.freedesktop.Secret.Prompt.Dismiss >/dev/null 2>&1 || true
      done
      count=$((count+1)); sleep .1
    done
  ) &
  supervisor=$!
  trap 'kill "$supervisor" 2>/dev/null || true' EXIT
  run "$1" "$2" retry
  kill "$supervisor" 2>/dev/null || true
  run "$1" "$2" read
  run "$1" "$2" corrupt
else
  run "$1" "$2" "$3"
fi
