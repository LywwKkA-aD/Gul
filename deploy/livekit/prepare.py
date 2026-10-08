#!/usr/bin/env python3
"""Generate a private single-VPS LiveKit deployment behind TLS on port 443."""

import argparse
import hashlib
import ipaddress
import json
import os
import re
from pathlib import Path
import secrets


def server_ip(value):
    ip = ipaddress.IPv4Address(value)
    if ip.is_loopback or ip.is_link_local or ip.is_unspecified or ip.is_multicast:
        raise ValueError('a public IPv4 server address is required')
    return str(ip)


def sfu_config(address, key, secret):
    ip = ipaddress.ip_network(address + '/32')
    # A signed-in participant may relay to this SFU only, not to arbitrary
    # internet hosts or services on the private network.
    deny = [str(net) for net in ipaddress.ip_network('0.0.0.0/0').address_exclude(ip)]
    return {
        'port': 7880,
        'rtc': {
            'tcp_port': 7881, 'udp_port': 7882, 'node_ip': address,
            'use_external_ip': False, 'enable_loopback_candidate': False,
            'ips': {'includes': [address + '/32']},
            'stun_servers': [address + ':7882'],
            'allow_tcp_fallback': True,
        },
        'turn': {
            'enabled': True, 'domain': address, 'tls_port': 5349, 'udp_port': 0,
            'external_tls': True, 'proxy_protocol': True,
            'proxy_protocol_trusted_cidrs': ['127.0.0.0/8'],
            'deny_peer_cidrs': deny + ['::/0'],
            'per_user_relay_allocation_limit': 8,
        },
        'keys': {key: secret},
        'logging': {'level': 'warn', 'pion_level': 'error'},
        'room': {'max_participants': 16, 'empty_timeout': 60, 'departure_timeout': 20},
        'limit': {'subscription_limit_video': 4, 'subscription_limit_audio': 16},
    }


def dns_name(value):
    value = value.lower().rstrip('.')
    try:
        ipaddress.ip_address(value)
    except ValueError:
        pass
    else:
        raise ValueError('the REALITY server name must be a DNS name')
    if len(value) > 253 or not all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', part)
                                   for part in value.split('.')):
        raise ValueError('invalid REALITY server name')
    return value


def proxy_config(address, reality_sni=None, managed_channels=False):
    ingress = ''
    tls_bind = '0.0.0.0:443'
    if reality_sni is not None:
        reality_sni = dns_name(reality_sni)
        tls_bind = '127.0.0.1:9443 accept-proxy'
        ingress = '''frontend gul_ingress
    bind 0.0.0.0:443
    tcp-request inspect-delay 5s
    acl tls_client_hello req.ssl_hello_type 1
    acl reality_sni req.ssl_sni -i @SNI@
    tcp-request content accept if tls_client_hello
    tcp-request content reject if WAIT_END
    use_backend gul_reality_transport if reality_sni
    default_backend gul_tls_transport

backend gul_reality_transport
    server local_reality 127.0.0.1:8443

backend gul_tls_transport
    server local_tls 127.0.0.1:9443 send-proxy-v2

'''.replace('@SNI@', reality_sni)
    return '''# TLS terminates once; HTTP/WebSocket and TURN share TCP 443.
# Do not enable HTTP/access logs: signal URLs can contain participant JWTs.
global
    user haproxy
    group haproxy
    maxconn 512
    ssl-default-bind-options ssl-min-ver TLSv1.2

defaults
    mode tcp
    timeout connect 5s
    timeout client 1h
    timeout server 1h

@INGRESS@frontend gul_tls
    bind @TLS_BIND@ ssl crt /etc/haproxy/gul.pem alpn http/1.1
    tcp-request inspect-delay 5s
    acl turn_stun req.payload(4,4) -m bin 2112a442
    tcp-request content accept if HTTP
    tcp-request content accept if turn_stun
    tcp-request content reject if WAIT_END
    use_backend gul_http_transport if HTTP
    default_backend gul_turn_transport

backend gul_http_transport
    server local_http 127.0.0.1:8080 send-proxy-v2

backend gul_turn_transport
    server local_turn 127.0.0.1:5349 send-proxy-v2

frontend gul_http
    mode http
    bind 127.0.0.1:8080 accept-proxy
    timeout http-request 10s
    timeout http-keep-alive 30s
    acl valid_host hdr(host) -i @IP@ @IP@:443
    http-request deny unless valid_host
    http-request set-header Host @IP@
    http-request del-header Forwarded
    http-request set-header X-Forwarded-For %[src]
    http-request set-header X-Forwarded-Proto https
    acl gul_api path_beg /api/gul/
    acl rtc path /rtc /rtc/validate /rtc/v1 /rtc/v1/validate
    acl health path /healthz
    acl root path /
    http-request deny deny_status 404 unless gul_api or rtc or health or root
    http-request return status 200 content-type text/plain string "Gul LiveKit is online." if root
    @BROKER_ROUTE@
    default_backend @DEFAULT_BACKEND@

backend gul_broker
    mode http
    timeout server 15s
    timeout tunnel 1h
    server broker 127.0.0.1:8787

@SIGNAL_BACKEND@
'''.replace('@IP@', server_ip(address)).replace('@INGRESS@', ingress).replace('@TLS_BIND@', tls_bind).replace(
        '@BROKER_ROUTE@', 'use_backend gul_broker if gul_api or rtc or health' if managed_channels else
        'use_backend gul_broker if gul_api or health').replace(
        '@DEFAULT_BACKEND@', 'gul_broker' if managed_channels else 'gul_signal').replace(
        '@SIGNAL_BACKEND@', '' if managed_channels else '''backend gul_signal
    mode http
    timeout tunnel 1h
    server signal 127.0.0.1:7880''')


def write_private(path, content):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as file:
        file.write(content)


def generate(address, password, output, managed_channels=False):
    address = server_ip(address)
    if len(password) < 16 or len(password) > 256 or any(ch.isspace() for ch in password):
        raise ValueError('use a generated single-line join password of 16 to 256 characters')
    if output.exists():
        raise FileExistsError('output already exists; credentials will not be replaced')
    key, secret = 'gul-' + secrets.token_hex(12), secrets.token_hex(32)
    broker = {
        'listenAddress': '127.0.0.1:8787',
        'publicOrigin': 'https://' + address, 'liveKitURL': 'wss://' + address,
        'liveKitInternalURL': 'http://127.0.0.1:7880',
        'apiKey': key, 'apiSecret': secret,
        'joinPasswordSHA256': hashlib.sha256(password.encode('utf-8')).hexdigest(),
    }
    if managed_channels:
        # DynamicUser exposes /var/lib/gul-livekit as a symlink. Use its
        # canonical private directory so the catalogue's no-symlink guard holds.
        broker['statePath'] = '/var/lib/private/gul-livekit/catalog.json'
    output.mkdir(parents=True, mode=0o700)
    os.chmod(output, 0o700)
    write_private(output / 'broker.json', json.dumps(broker, indent=2) + '\n')
    write_private(output / 'livekit.yaml', json.dumps(sfu_config(address, key, secret), indent=2) + '\n')
    write_private(output / 'haproxy.cfg', proxy_config(address, managed_channels=managed_channels))
    write_private(output / 'address', 'https://' + address + '\n')
    write_private(output / 'Gul-LiveKit-server.txt',
                  'Gul LiveKit\n\nАдрес: https://' + address +
                  '\nПароль сервера: ' + password + '\nНик: выберите свой.\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('server_ip')
    parser.add_argument('--password-file', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--managed-channels', action='store_true')
    args = parser.parse_args()
    password = args.password_file.read_text(encoding='utf-8').removesuffix('\n').removesuffix('\r')
    generate(args.server_ip, password, args.output, managed_channels=args.managed_channels)
    print('Prepared private LiveKit configuration; no credentials printed.')


if __name__ == '__main__':
    main()
