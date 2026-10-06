#!/usr/bin/env python3
"""Prepare a private, fixed-destination VLESS/REALITY profile for Gul."""

import argparse
import base64
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import uuid
from urllib.parse import urlencode


def user_id(password):
    digest = bytearray(hashlib.sha256(
        b'gul/vless-reality/user-id/v1\0' + password.encode('utf-8')
    ).digest()[:16])
    digest[6] = (digest[6] & 15) | 128
    digest[8] = (digest[8] & 63) | 128
    return str(uuid.UUID(bytes=bytes(digest)))


def dns_name(value):
    value = value.lower().rstrip('.')
    try:
        ipaddress.ip_address(value)
    except ValueError:
        pass
    else:
        raise ValueError('the REALITY server name must be a DNS name')
    if len(value) > 253 or not all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', p) for p in value.split('.')):
        raise ValueError('invalid REALITY server name')
    return value


def address(host, sni, public_key, short_id):
    ip = ipaddress.ip_address(host)
    sni = dns_name(sni)
    if not re.fullmatch(r'[A-Za-z0-9_-]{43}', public_key) or len(base64.urlsafe_b64decode(public_key + '=')) != 32:
        raise ValueError('invalid REALITY public key')
    if not re.fullmatch(r'(?:[0-9a-f]{2}){1,8}', short_id):
        raise ValueError('invalid REALITY short ID')
    authority = '[' + str(ip) + ']' if ip.version == 6 else str(ip)
    params = dict(flow='none', pbk=public_key, security='reality', sid=short_id, sni=sni, type='tcp')
    return 'vless://' + authority + '?' + urlencode(params)


def server_config(password, sni, private_key, short_id):
    sni = dns_name(sni)
    return {
        'log': {'loglevel': 'error', 'access': 'none'},
        'inbounds': [{
            'tag': 'gul-reality', 'listen': '0.0.0.0', 'port': 443,
            'protocol': 'vless',
            'settings': {'clients': [{'id': user_id(password), 'flow': ''}], 'decryption': 'none'},
            'streamSettings': {'network': 'tcp', 'security': 'reality', 'realitySettings': {
                'show': False, 'target': sni + ':443', 'xver': 0,
                'serverNames': [sni], 'privateKey': private_key, 'shortIds': [short_id],
            }},
        }],
        'outbounds': [
            {'tag': 'blocked', 'protocol': 'blackhole'},
            {'tag': 'mumble', 'protocol': 'freedom', 'settings': {'redirect': '127.0.0.1:64738'}},
        ],
        'routing': {'domainStrategy': 'AsIs', 'rules': [
            {'type': 'field', 'inboundTag': ['gul-reality'], 'network': 'tcp',
             'ip': ['127.0.0.1/32'], 'port': '64738', 'outboundTag': 'mumble'},
            {'type': 'field', 'inboundTag': ['gul-reality'], 'outboundTag': 'blocked'},
        ]},
    }


def parse_keys(output):
    values = dict(line.split(':', 1) for line in output.splitlines() if ':' in line)
    private = values.get('PrivateKey', '').strip()
    public = values.get('Password (PublicKey)', values.get('PublicKey', values.get('Password', ''))).strip()
    if not private or not public:
        raise ValueError('Xray returned an unsupported key format')
    return private, public


def write_private(path, content):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8') as output:
        output.write(content)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('server_ip')
    parser.add_argument('server_name')
    parser.add_argument('--join-password-file', type=Path, required=True)
    parser.add_argument('--xray', type=Path, required=True)
    parser.add_argument('--output', type=Path, default=Path('private/reality'))
    args = parser.parse_args()
    # Validate metadata before generating keys or creating output files.
    ipaddress.ip_address(args.server_ip)
    sni = dns_name(args.server_name)
    password = args.join_password_file.read_text(encoding='utf-8').removesuffix('\n').removesuffix('\r')
    if len(password) < 16 or '\n' in password or '\r' in password:
        raise ValueError('use a generated join password with at least 16 characters on one line')
    if args.output.exists():
        raise FileExistsError('output already exists; existing REALITY keys will not be replaced')
    generated = subprocess.run([str(args.xray.resolve()), 'x25519'], check=True, capture_output=True, text=True)
    private, public = parse_keys(generated.stdout)
    short_id = secrets.token_hex(8)
    profile = address(args.server_ip, sni, public, short_id)
    config = server_config(password, sni, private, short_id)
    args.output.mkdir(parents=True, mode=0o700)
    os.chmod(args.output, 0o700)
    write_private(args.output / 'server.json', json.dumps(config, indent=2) + '\n')
    write_private(args.output / 'address', profile + '\n')
    write_private(args.output / 'Gul-Reality-server.txt',
                  'Gul 0.6.0-alpha.3 — VLESS + REALITY (TCP 443)\n\nАдрес:\n' + profile +
                  '\n\nПароль сервера (прежний):\n' + password + '\n\nНик: любой свободный.\n')
    print('Prepared private REALITY configuration and Gul connection file; no credentials printed.')


if __name__ == '__main__':
    main()
