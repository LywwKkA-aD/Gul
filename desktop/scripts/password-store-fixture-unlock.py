"""Test-only PAM-style unlock of this runner's disposable GNOME daemon.

Protocol: GNOME/gnome-keyring 46.1 daemon/control/gkd-control-client.c.
Production Gul never accepts or transmits a keyring password.
"""
import os
import socket
import stat
import struct
import sys
from pathlib import Path

try:
    runtime = Path(os.environ["XDG_RUNTIME_DIR"])
    control = runtime / "keyring" / "control"
    metadata = control.lstat()
    assert stat.S_ISSOCK(metadata.st_mode) and metadata.st_uid == os.geteuid()
    assert Path(os.environ["GNOME_KEYRING_CONTROL"]) == control.parent
    password = os.environ["GUL_VAULT_FIXTURE_KEYRING_PASSWORD"].encode("utf-8")
    assert 0 < len(password) <= 128
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as peer:
        peer.settimeout(5)
        peer.connect(str(control))
        # A credential byte precedes the network-endian length/op/string packet.
        peer.sendall(b"\0" + struct.pack(">III", 12 + len(password), 1, len(password)) + password)
        response = b""
        while len(response) < 8:
            chunk = peer.recv(8 - len(response))
            assert chunk
            response += chunk
        assert struct.unpack(">II", response) == (8, 0)
except Exception:
    print("GUL_VAULT_FIXTURE_UNLOCK_FAILED", file=sys.stderr)
    sys.exit(1)
