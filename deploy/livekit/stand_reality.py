#!/usr/bin/env python3
"""Create an isolated local HAProxy + Xray + LiveKit fixture; no VPS is touched.

Requires Linux amd64 Xray 26.3.27 and gul-livekit-server binaries. The caller
builds Containerfile.smoke as gul-livekit-reality-smoke:local. Private files are
written only to a new output directory. Stop with --remove OUTPUT when done.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import time


spec = importlib.util.spec_from_file_location('prepare_livekit_reality', Path(__file__).with_name('prepare_reality.py'))
reality = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reality)
SFU_IMAGE = 'livekit/livekit-server:v1.13.8@sha256:ad961c9b4b064298cc08d618d772671f7c783e036001388079fa813929cc357e'


def command(*args):
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode:
        # Configs and diagnostic output may contain temporary credentials.
        raise RuntimeError('fixture command failed: ' + args[0])
    return result.stdout.strip()


def write(path, value):
    reality.base.write_private(path, value)


def create_certificate(container):
    # Bind-mounted private files must remain owned by the calling Linux user.
    command('docker', 'exec', '--user', f'{os.getuid()}:{os.getgid()}', container,
            'openssl', 'req', '-x509', '-newkey', 'rsa:2048',
            '-nodes', '-days', '2', '-subj', '/CN=Gul local fixture',
            '-addext', 'subjectAltName=IP:127.0.0.1,DNS:camouflage.example.org',
            '-keyout', '/work/tls.key', '-out', '/work/ca.pem')


def fixture_sfu_config(node_ip, key, secret):
    config = reality.base.sfu_config(node_ip, key, secret)
    config['turn']['domain'] = '127.0.0.1'
    # LiveKit rejects private TURN peers before applying deny_peer_cidrs.
    # The fixture shares one Docker IP; allow exactly that SFU, never its subnet.
    # https://github.com/livekit/livekit/blob/v1.13.8/pkg/service/turn.go
    config['turn']['allow_restricted_peer_cidrs'] = [node_ip + '/32']
    return config


def remove(output):
    names_file = output / 'containers.json'
    if not names_file.is_file():
        raise ValueError('fixture container inventory is missing')
    for name in json.loads(names_file.read_text()):
        if not name.startswith('gul-reality-test-'):
            raise ValueError('unexpected fixture container name')
        subprocess.run(['docker', 'rm', '-f', name], capture_output=True, check=False)
    print('Removed local fixture containers; private files remain in the requested output directory.')


def start(output, xray, broker, gateway_image):
    if output.exists():
        raise FileExistsError('fixture output already exists')
    output.mkdir(parents=True, mode=0o700)
    os.chmod(output, 0o700)
    output = output.resolve()
    prefix = 'gul-reality-test-' + secrets.token_hex(4)
    names = [prefix + '-sfu', prefix + '-gateway']
    write(output / 'containers.json', json.dumps(names))
    for src, name in [(xray, 'xray'), (broker, 'broker')]:
        shutil.copyfile(src, output / name)
        os.chmod(output / name, 0o700)
    try:
        command('docker', 'run', '-d', '--platform', 'linux/amd64', '--name', names[1],
                '-p', '127.0.0.1::443', '-v', str(output) + ':/work', gateway_image)
        inspect = json.loads(command('docker', 'inspect', names[1]))[0]
        port = inspect['NetworkSettings']['Ports']['443/tcp'][0]['HostPort']
        node_ip = inspect['NetworkSettings']['Networks']['bridge']['IPAddress']
        keys = command('docker', 'exec', names[1], '/work/xray', 'x25519')
        private, public = reality.parse_keys(keys)
        password = secrets.token_urlsafe(32)
        short_id = secrets.token_hex(8)
        sni = 'camouflage.example.org'
        reality.generate('203.0.113.9', password, sni, private, public, short_id, output / 'reality')
        profile = (output / 'reality/address').read_text().replace('203.0.113.9', '127.0.0.1:' + port)
        write(output / 'address', profile)
        write(output / 'join-password', password + '\n')
        config_path = output / 'reality/server.json'
        config = json.loads(config_path.read_text())
        # The isolated fixture uses a local TLS 1.3 decoy instead of the internet.
        config['inbounds'][0]['streamSettings']['realitySettings']['target'] = '127.0.0.1:9444'
        config_path.write_text(json.dumps(config))
        proxy = (output / 'reality/haproxy.cfg').read_text().replace('203.0.113.9', '127.0.0.1')
        write(output / 'haproxy.cfg', proxy)
        key, secret = 'fixture-' + secrets.token_hex(12), secrets.token_hex(32)
        sfu = fixture_sfu_config(node_ip, key, secret)
        write(output / 'livekit.yaml', json.dumps(sfu))
        broker_config = {
            'listenAddress': '127.0.0.1:8787', 'publicOrigin': 'https://127.0.0.1',
            'liveKitURL': 'wss://127.0.0.1', 'liveKitInternalURL': 'http://127.0.0.1:7880',
            'apiKey': key, 'apiSecret': secret,
            'joinPasswordSHA256': reality.hashlib.sha256(password.encode()).hexdigest(),
        }
        write(output / 'broker.json', json.dumps(broker_config))
        create_certificate(names[1])
        os.chmod(output / 'tls.key', 0o600)
        os.chmod(output / 'ca.pem', 0o600)
        command('docker', 'exec', names[1], 'sh', '-c',
                'umask 077; cat /work/ca.pem /work/tls.key > /etc/haproxy/gul.pem')
        command('docker', 'exec', names[1], 'haproxy', '-c', '-f', '/work/haproxy.cfg')
        command('docker', 'exec', names[1], '/work/xray', 'run', '-test', '-config', '/work/reality/server.json')
        command('docker', 'exec', '-d', names[1], 'openssl', 's_server', '-accept', '9444',
                '-cert', '/work/ca.pem', '-key', '/work/tls.key', '-tls1_3', '-alpn', 'h2', '-no_ticket', '-quiet')
        command('docker', 'exec', '-d', names[1], 'haproxy', '-db', '-f', '/work/haproxy.cfg')
        command('docker', 'exec', '-d', names[1], '/work/xray', 'run', '-config', '/work/reality/server.json')
        command('docker', 'run', '-d', '--name', names[0], '--network', 'container:' + names[1],
                '-v', str(output / 'livekit.yaml') + ':/etc/livekit.yaml:ro',
                SFU_IMAGE, '--config', '/etc/livekit.yaml')
        command('docker', 'exec', '-d', names[1], '/work/broker', '-config', '/work/broker.json')
        for attempt in range(30):
            try:
                result = command('docker', 'exec', names[1], 'curl', '--fail', '--silent', '--show-error',
                                 '--max-time', '2', '--cacert', '/work/ca.pem', 'https://127.0.0.1/healthz')
                if json.loads(result).get('status') == 'ok':
                    break
            except (RuntimeError, json.JSONDecodeError):
                pass
            time.sleep(0.2)
        else:
            raise RuntimeError('fixture HTTPS health did not become ready')
        print('Local REALITY/HTTPS/SFU fixture is ready. Private inputs: ' + str(output))
    except BaseException:
        remove(output)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--remove', type=Path)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--xray', type=Path)
    parser.add_argument('--broker', type=Path)
    parser.add_argument('--gateway-image', default='gul-livekit-reality-smoke:local')
    args = parser.parse_args()
    if args.remove:
        remove(args.remove)
    elif args.output and args.xray and args.broker:
        start(args.output, args.xray, args.broker, args.gateway_image)
    else:
        parser.error('--output, --xray and --broker are required to start a fixture')


if __name__ == '__main__':
    main()
