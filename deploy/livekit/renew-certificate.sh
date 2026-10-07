#!/bin/sh
set -eu
/usr/bin/docker run --rm --name gul-livekit-certbot-renew \
    -p 80:80 --memory 256m --security-opt no-new-privileges:true \
    -v /opt/gul-livekit/acme:/etc/letsencrypt \
    -v /opt/gul-livekit/acme-work:/var/lib/letsencrypt \
    -v /opt/gul-livekit/acme-logs:/var/log/letsencrypt \
    certbot/certbot:v5.8.0@sha256:f70ad0adbb7e117f0fe42a63c553f28ea451edabc0148757b6efcd9735acaa20 \
    renew --cert-name gul-ip --non-interactive --quiet --no-random-sleep-on-renew "$@"
for argument in "$@"; do
    if [ "$argument" = '--dry-run' ]; then
        exit 0
    fi
done
/opt/gul-livekit/update-certificate.sh
