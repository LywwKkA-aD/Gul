#!/bin/sh
set -eu
# This runs only in the test's new D-Bus session and isolated XDG directories.
printf '%s' 'gul-fixture-unlock' | gnome-keyring-daemon --unlock --components=secrets >/dev/null 2>&1
if test "$3" = write; then
  "$1" --no-sandbox --disable-gpu --password-store=gnome-libsecret "$2" --gul-keyring-phase=write
fi
"$1" --no-sandbox --disable-gpu --password-store=gnome-libsecret "$2" --gul-keyring-phase=read
