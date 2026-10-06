#!/usr/bin/env python3
"""Create a private Hysteria + Mumble deployment without printing credentials."""

import argparse
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets


def validate(domain, email, obfs, ip_certificate=None):
    try:
        address = ipaddress.ip_address(domain)
    except ValueError:
        address = None
    if ip_certificate is not None:
        # Match the fixed certificate directory mounted by compose.ip.yaml.
        if ip_certificate != "gul-ip":
            raise ValueError("The external IP certificate must be named gul-ip")
        if (address is None or not address.is_global or address.is_multicast
                or address.is_reserved or "%" in domain):
            raise ValueError("Use a literal public IP address without a port, zone or URL")
    else:
        labels = domain.split(".")
        if (address is not None or len(domain) > 253 or len(labels) < 2 or not all(
            re.fullmatch(r"[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?", label)
            for label in labels
        )):
            raise ValueError("Use a DNS hostname, or --ip-certificate gul-ip for a public IP")
    if (ip_certificate is None or email is not None) and not re.fullmatch(
        r"[^\s@]+@[^\s@]+\.[^\s@]+", email or ""
    ):
        raise ValueError("A valid ACME account email is required")
    if obfs not in ("none", "salamander", "gecko"):
        raise ValueError("Obfuscation must be none, salamander or gecko")


def write_private(path, content):
    # O_EXCL prevents replacing an existing file or following a symlink.
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as output:
        output.write(content)


def prepare(directory, domain, email, obfs, ip_certificate=None):
    validate(domain, email, obfs, ip_certificate)
    directory.mkdir(mode=0o700)
    password = secrets.token_hex(24)
    certificate = {
        "tls": {
            "cert": "/etc/letsencrypt/live/gul-ip/fullchain.pem",
            "key": "/etc/letsencrypt/live/gul-ip/privkey.pem",
            "sniGuard": "dns-san",
        },
    } if ip_certificate is not None else {
        "acme": {
            "domains": [domain], "email": email,
            "ca": "letsencrypt", "type": "http", "dir": "/acme",
        },
    }
    config = {
        "listen": ":443",
        **certificate,
        "auth": {"type": "password", "password": password},
        "disableUDP": True,
        # Pin the actual dial target too: a hostname may resolve to both
        # loopback IPv4 and an unrelated IPv6 address that matched no rule.
        "acl": {"inline": ["direct(127.0.0.1, tcp/64738, 127.0.0.1)", "reject(all)"]},
        "quic": {"maxIncomingStreams": 8},
        "masquerade": {
            "type": "string",
            "string": {
                "content": "<!doctype html><title>Welcome</title><h1>Welcome</h1>\n",
                "headers": {"content-type": "text/html; charset=utf-8"},
                "statusCode": 200,
            },
            "listenHTTPS": ":443",
        },
    }
    if obfs != "none":
        config = {**config, "obfs": {"type": obfs, obfs: {"password": password}}}
    suffix = "" if obfs == "none" else f"?obfs={obfs}"
    authority = f"[{domain}]" if ":" in domain else domain
    write_private(directory / "server.json", json.dumps(config, indent=2) + "\n")
    write_private(directory / "join-password", password + "\n")
    write_private(directory / "admin-password", secrets.token_hex(24) + "\n")
    write_private(directory / "client-address", f"hysteria2://{authority}{suffix}\n")
    print(f"Prepared {directory}. Credentials are stored in private files; no services were started.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("domain", help="DNS hostname or public IP of this VPS")
    parser.add_argument("email", nargs="?", help="ACME account email (required for DNS mode)")
    parser.add_argument("--ip-certificate", choices=("gul-ip",),
                        help="Use an externally renewed public CA certificate named gul-ip")
    parser.add_argument("--obfs", choices=("none", "salamander", "gecko"), default="none")
    parser.add_argument("--output-dir", type=Path,
                        default=Path(__file__).resolve().parent / "private")
    args = parser.parse_args()
    try:
        prepare(args.output_dir, args.domain, args.email, args.obfs, args.ip_certificate)
    except (OSError, ValueError) as error:
        parser.exit(1, f"Setup failed: {error}\n")


if __name__ == "__main__":
    main()
