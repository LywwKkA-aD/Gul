#!/bin/sh
set -eu
umask 077
base=/opt/gul-livekit/acme/live/gul-ip
openssl x509 -in "$base/fullchain.pem" -noout -checkend 0 >/dev/null
temporary=$(mktemp /etc/haproxy/.gul-cert.XXXXXX)
trap 'rm -f "$temporary"' EXIT
cat "$base/fullchain.pem" "$base/privkey.pem" > "$temporary"
# The active file is replaced atomically; HAProxy reload preserves sessions.
mv "$temporary" /etc/haproxy/gul.pem
haproxy -c -f /etc/haproxy/haproxy.cfg >/dev/null
systemctl reload haproxy
