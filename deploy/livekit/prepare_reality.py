#!/usr/bin/env python3
"""Add a private fixed-destination REALITY transport to an existing LiveKit VPS."""

import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import uuid
from urllib.parse import urlencode


spec = importlib.util.spec_from_file_location('livekit_base_prepare', Path(__file__).with_name('prepare.py'))
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
proxy_config = base.proxy_config


def user_id(password):
    digest = bytearray(hashlib.sha256(b'gul/vless-reality/user-id/v1\0' + password.encode('utf-8')).digest()[:16])
    digest[6] = (digest[6] & 15) | 128
    digest[8] = (digest[8] & 63) | 128
    return str(uuid.UUID(bytes=bytes(digest)))


def key32(value):
    if not re.fullmatch(r'[A-Za-z0-9_-]{43}', value):
        raise ValueError('invalid REALITY key')
    decoded = base64.urlsafe_b64decode(value + '=')
    if len(decoded) != 32 or base64.urlsafe_b64encode(decoded).decode().rstrip('=') != value:
        raise ValueError('invalid REALITY key')
    return value


def validate_short_id(value):
    if not re.fullmatch(r'(?:[0-9a-f]{2}){1,8}', value):
        raise ValueError('invalid REALITY short ID')
    return value


def address(host, sni, public_key, short_id):
    host = base.server_ip(host)
    params = dict(flow='none', pbk=key32(public_key), security='reality',
                  sid=validate_short_id(short_id), sni=base.dns_name(sni), type='tcp')
    return 'livekit+vless://' + host + '?' + urlencode(params)


def server_config(password, sni, private_key, short_id):
    sni = base.dns_name(sni)
    return {
        'log': {'loglevel': 'error', 'access': 'none'},
        'inbounds': [{
            'tag': 'gul-reality', 'listen': '127.0.0.1', 'port': 8443,
            'protocol': 'vless',
            'settings': {'clients': [{'id': user_id(password), 'flow': ''}], 'decryption': 'none'},
            'streamSettings': {'network': 'tcp', 'security': 'reality', 'realitySettings': {
                'show': False, 'target': sni + ':443', 'xver': 0,
                'serverNames': [sni], 'privateKey': key32(private_key),
                'shortIds': [validate_short_id(short_id)],
            }},
        }],
        'outbounds': [
            {'tag': 'blocked', 'protocol': 'blackhole'},
            {'tag': 'gul-tls', 'protocol': 'freedom', 'settings': {'redirect': '127.0.0.1:443'}},
        ],
        'routing': {'domainStrategy': 'AsIs', 'rules': [
            {'type': 'field', 'inboundTag': ['gul-reality'], 'network': 'tcp',
             'ip': ['127.0.0.1/32'], 'port': '443', 'outboundTag': 'gul-tls'},
            {'type': 'field', 'inboundTag': ['gul-reality'], 'outboundTag': 'blocked'},
        ]},
    }


def parse_keys(output):
    values = dict(line.split(':', 1) for line in output.splitlines() if ':' in line)
    private = values.get('PrivateKey', '').strip()
    public = values.get('Password (PublicKey)', values.get('PublicKey', values.get('Password', ''))).strip()
    return key32(private), key32(public)


def generate(host, password, sni, private_key, public_key, short_id, output, managed_channels=False):
    if len(password) < 16 or len(password) > 256 or any(ch.isspace() for ch in password):
        raise ValueError('use a generated single-line join password of 16 to 256 characters')
    profile = address(host, sni, public_key, short_id)
    config = server_config(password, sni, private_key, short_id)
    proxy = proxy_config(host, sni, managed_channels=managed_channels)
    if output.exists():
        raise FileExistsError('output already exists; REALITY credentials will not be replaced')
    output.mkdir(parents=True, mode=0o700)
    os.chmod(output, 0o700)
    base.write_private(output / 'server.json', json.dumps(config, indent=2) + '\n')
    base.write_private(output / 'address', profile + '\n')
    base.write_private(output / 'haproxy.cfg', proxy)
    base.write_private(output / 'Gul-LiveKit-Reality-server.txt',
                       'Gul LiveKit — VLESS + REALITY (TCP 443)\n\nАдрес:\n' + profile +
                       '\n\nПароль сервера (прежний):\n' + password + '\n\nНик: выберите свой.\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('server_ip')
    parser.add_argument('server_name')
    parser.add_argument('--password-file', type=Path, required=True)
    parser.add_argument('--xray', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--managed-channels', action='store_true')
    args = parser.parse_args()
    base.server_ip(args.server_ip)
    base.dns_name(args.server_name)
    if args.output.exists():
        raise FileExistsError('output already exists; REALITY credentials will not be replaced')
    password = args.password_file.read_text(encoding='utf-8').removesuffix('\n').removesuffix('\r')
    keys = subprocess.run([str(args.xray.resolve()), 'x25519'], check=True, capture_output=True, text=True)
    private, public = parse_keys(keys.stdout)
    generate(args.server_ip, password, args.server_name, private, public, secrets.token_hex(8), args.output,
             managed_channels=args.managed_channels)
    print('Prepared private LiveKit REALITY files; no credentials printed.')


if __name__ == '__main__':
    main()
