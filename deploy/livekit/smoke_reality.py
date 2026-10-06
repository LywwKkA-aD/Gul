#!/usr/bin/env python3
"""Verify the local stand's HTTPS/TURN multiplexing, REALITY and destination ACL."""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time
from urllib.parse import parse_qs, urlparse


spec = importlib.util.spec_from_file_location('local_reality_stand', Path(__file__).with_name('stand_reality.py'))
stand = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stand)

TURN_PROBE = r'''
import socket, ssl, struct, sys

def read_exact(sock, n):
    data = b''
    while len(data) < n:
        part = sock.recv(n - len(data))
        if not part:
            raise RuntimeError('unexpected connection end')
        data += part
    return data

sock = socket.create_connection(('127.0.0.1', int(sys.argv[1])), timeout=8)
if sys.argv[1] != '443':
    sock.sendall(bytes.fromhex('050100'))
    assert read_exact(sock, 2) == bytes.fromhex('0500')
    sock.sendall(bytes.fromhex('050100017f00000101bb'))
    header = read_exact(sock, 4)
    assert header[:2] == bytes.fromhex('0500')
    assert header[3] == 1
    read_exact(sock, 6)
context = ssl.create_default_context(cafile='/work/ca.pem')
context.set_alpn_protocols(['http/1.1'])
secure = context.wrap_socket(sock, server_hostname='127.0.0.1')
transaction = b'gulTURNprobe'
secure.sendall(struct.pack('!HHI', 1, 0, 0x2112A442) + transaction)
header = read_exact(secure, 20)
kind, size, cookie = struct.unpack('!HHI', header[:8])
assert cookie == 0x2112A442 and header[8:] == transaction
assert kind == 0x0101, 'expected successful STUN binding response'
read_exact(secure, size)
secure.close()
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('fixture', type=Path)
    args = parser.parse_args()
    fixture = args.fixture.resolve()
    gateway = json.loads((fixture / 'containers.json').read_text())[1]
    profile = urlparse((fixture / 'address').read_text().strip())
    fields = parse_qs(profile.query)
    password = (fixture / 'join-password').read_text().rstrip('\n')
    config = {
        'log': {'loglevel': 'error', 'access': 'none'},
        'inbounds': [{'listen': '127.0.0.1', 'port': 1080, 'protocol': 'socks',
                      'settings': {'auth': 'noauth', 'udp': False}}],
        'outbounds': [{'protocol': 'vless', 'settings': {'vnext': [{
            'address': '127.0.0.1', 'port': 443,
            'users': [{'id': stand.reality.user_id(password), 'encryption': 'none', 'flow': ''}],
        }]}, 'streamSettings': {'network': 'tcp', 'security': 'reality', 'realitySettings': {
            'serverName': fields['sni'][0], 'fingerprint': 'chrome',
            'publicKey': fields['pbk'][0], 'shortId': fields['sid'][0],
        }}}],
    }
    path = fixture / 'probe-client.json'
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as file:
        json.dump(config, file)
    stand.command('docker', 'exec', gateway, '/work/xray', 'run', '-test', '-config', '/work/probe-client.json')
    stand.command('docker', 'exec', '-d', gateway, 'sh', '-c',
                  'echo $$ > /work/probe-client.pid; exec /work/xray run -config /work/probe-client.json')
    try:
        for attempt in range(30):
            try:
                reply = stand.command('docker', 'exec', gateway, 'curl', '--fail', '--silent', '--show-error',
                                      '--max-time', '3', '--noproxy', '', '--proxy', 'socks5h://127.0.0.1:1080',
                                      '--cacert', '/work/ca.pem', 'https://127.0.0.1/healthz')
                if json.loads(reply).get('status') == 'ok':
                    break
            except (RuntimeError, json.JSONDecodeError):
                pass
            time.sleep(0.2)
        else:
            raise RuntimeError('REALITY HTTPS health failed')
        print('PASS: REALITY → fixed local TLS → HTTPS broker, with certificate verification.')
        for port, name in [('443', 'direct HTTPS/TURN'), ('1080', 'REALITY HTTPS/TURN')]:
            stand.command('docker', 'exec', gateway, 'python3', '-c', TURN_PROBE, port)
            print('PASS: ' + name + ' shares port 443 and returns a real TURN STUN response.')
        blocked = subprocess.run(['docker', 'exec', gateway, 'curl', '--silent', '--show-error',
                                  '--max-time', '4', '--noproxy', '', '--proxy', 'socks5h://127.0.0.1:1080',
                                  'http://127.0.0.1:8787/healthz'], capture_output=True)
        if blocked.returncode in (0, 28):
            raise RuntimeError('destination ACL check allowed access or timed out')
        print('PASS: authenticated REALITY cannot reach broker plaintext port 8787 directly.')
        blocked_address = subprocess.run(['docker', 'exec', gateway, 'curl', '--silent', '--show-error',
                                          '--max-time', '4', '--noproxy', '', '--proxy', 'socks5h://127.0.0.1:1080',
                                          'https://127.0.0.2/healthz'], capture_output=True)
        if blocked_address.returncode in (0, 28):
            raise RuntimeError('destination address ACL check allowed access or timed out')
        print('PASS: authenticated REALITY cannot select another loopback address.')
        config['inbounds'][0]['port'] = 1081
        config['outbounds'][0]['settings']['vnext'][0]['users'][0]['id'] = stand.reality.user_id(password + '-wrong')
        path = fixture / 'probe-wrong.json'
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, 'w') as file:
            json.dump(config, file)
        stand.command('docker', 'exec', '-d', gateway, 'sh', '-c',
                      'echo $$ > /work/probe-wrong.pid; exec /work/xray run -config /work/probe-wrong.json')
        time.sleep(0.3)
        denied = subprocess.run(['docker', 'exec', gateway, 'curl', '--silent', '--show-error',
                                 '--max-time', '4', '--noproxy', '', '--proxy', 'socks5h://127.0.0.1:1081',
                                 '--cacert', '/work/ca.pem', 'https://127.0.0.1/healthz'], capture_output=True)
        if denied.returncode in (0, 7, 28):
            raise RuntimeError('wrong-password check allowed access or was inconclusive')
        print('PASS: correct REALITY metadata with the wrong Gul password cannot reach HTTPS.')
    finally:
        stand.command('docker', 'exec', gateway, 'sh', '-c',
                      'kill "$(cat /work/probe-client.pid)"; if test -f /work/probe-wrong.pid; then kill "$(cat /work/probe-wrong.pid)"; fi')


if __name__ == '__main__':
    main()
