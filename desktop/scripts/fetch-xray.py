#!/usr/bin/env python3
"""Fetch a pinned official Xray executable after checking its archive digest."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import platform
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def fetch(target):
    manifest = json.loads((ROOT / 'resources/xray/manifest.json').read_text())
    asset = manifest['assets'].get(target)
    if asset is None:
        raise ValueError('unsupported Xray platform')
    url = 'https://github.com/XTLS/Xray-core/releases/download/v' + manifest['version'] + '/' + asset['name']
    with urllib.request.urlopen(url, timeout=60) as response:
        data = response.read(50 * 1024 * 1024 + 1)
    if len(data) > 50 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != asset['sha256']:
        raise ValueError('Xray archive digest mismatch')
    executable = 'xray.exe' if target.startswith('win32-') else 'xray'
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        binary = archive.read(executable)
        license_text = archive.read('LICENSE')
    output = ROOT / 'resources/xray' / target
    output.mkdir(parents=True, exist_ok=True)
    destination = output / executable
    destination.write_bytes(binary)
    destination.chmod(0o755)
    (output / 'LICENSE').write_bytes(license_text)
    (output / 'SHA256').write_text(hashlib.sha256(binary).hexdigest() + '\n')
    print('Verified Xray ' + manifest['version'] + ' for ' + target)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    system = {'Darwin': 'darwin', 'Linux': 'linux', 'Windows': 'win32'}[platform.system()]
    architecture = 'arm64' if platform.machine().lower() in ('arm64', 'aarch64') else 'x64'
    parser.add_argument('--target', default=system + '-' + architecture)
    fetch(parser.parse_args().target)
