"""Exercise generated credentials and the server deployment boundary offline."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("prepare", ROOT / "prepare.py")
prepare = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(prepare)


class PrepareTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name) / "private"

    def generate(self, obfs="none"):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            prepare.prepare(self.directory, "voice.example.org", "admin@example.org", obfs)
        return json.loads((self.directory / "server.json").read_text()), output.getvalue()

    def test_passwords_agree_without_leaking_into_output_or_uri(self):
        config, output = self.generate()
        join = (self.directory / "join-password").read_text().strip()
        admin = (self.directory / "admin-password").read_text().strip()
        self.assertEqual(len(join), 48)
        self.assertNotEqual(join, admin)
        self.assertEqual(config["auth"], {"type": "password", "password": join})
        self.assertNotIn(join, output)
        self.assertNotIn(admin, output)
        self.assertEqual((self.directory / "client-address").read_text(),
                         "hysteria2://voice.example.org\n")
        self.assertNotIn("obfs", config)

    def test_obfuscation_matches_client_and_auth(self):
        for obfs in ("salamander", "gecko"):
            with self.subTest(obfs=obfs):
                self.directory = Path(self.temporary.name) / obfs
                config, _ = self.generate(obfs)
                self.assertEqual(config["obfs"]["type"], obfs)
                self.assertEqual(config["obfs"][obfs]["password"], config["auth"]["password"])
                self.assertEqual((self.directory / "client-address").read_text(),
                                 f"hysteria2://voice.example.org?obfs={obfs}\n")

    def test_credentials_are_private_even_with_permissive_umask(self):
        previous = os.umask(0)
        try:
            self.generate()
        finally:
            os.umask(previous)
        self.assertEqual(self.directory.stat().st_mode & 0o777, 0o700)
        for path in self.directory.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_repeated_setup_cannot_replace_existing_identity_or_password(self):
        self.generate()
        before = {path.name: path.read_bytes() for path in self.directory.iterdir()}
        with self.assertRaises(FileExistsError):
            self.generate()
        self.assertEqual(before, {path.name: path.read_bytes() for path in self.directory.iterdir()})

    def test_symlink_destination_is_refused(self):
        target = Path(self.temporary.name) / "target"
        target.mkdir()
        self.directory.symlink_to(target, target_is_directory=True)
        with self.assertRaises(FileExistsError):
            self.generate()
        self.assertEqual(list(target.iterdir()), [])

    def test_acl_only_exposes_mumble_and_acme_is_explicit(self):
        config, _ = self.generate()
        self.assertEqual(config["acl"]["inline"],
                         ["direct(127.0.0.1, tcp/64738, 127.0.0.1)", "reject(all)"])
        self.assertIs(config["disableUDP"], True)
        self.assertEqual(config["acme"]["domains"], ["voice.example.org"])
        self.assertEqual(config["acme"]["type"], "http")
        self.assertNotIn("tls", config)
        self.assertNotIn("sniff", config)

    def test_invalid_inputs_leave_no_credentials(self):
        for domain, email, obfs in (
            ("https://voice.example.org", "admin@example.org", "none"),
            ("voice.example.org/path", "admin@example.org", "none"),
            ("voice.example.org", "not-an-email", "none"),
            ("voice.example.org", "admin@example.org", "unknown"),
            ("voice.example.org", None, "none"),
            ("8.8.8.8", "admin@example.org", "none"),
        ):
            with self.subTest(domain=domain, email=email, obfs=obfs):
                with self.assertRaises(ValueError):
                    prepare.prepare(self.directory, domain, email, obfs)
                self.assertFalse(self.directory.exists())

    def test_ip_certificate_has_public_ca_paths_and_matching_passwords(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            prepare.prepare(self.directory, "8.8.8.8", None, "salamander",
                            ip_certificate="gul-ip")
        config = json.loads((self.directory / "server.json").read_text())
        self.assertNotIn("acme", config)
        self.assertEqual(config["tls"], {
            "cert": "/etc/letsencrypt/live/gul-ip/fullchain.pem",
            "key": "/etc/letsencrypt/live/gul-ip/privkey.pem",
            "sniGuard": "dns-san",
        })
        password = (self.directory / "join-password").read_text().strip()
        self.assertEqual(config["auth"]["password"], password)
        self.assertEqual(config["obfs"]["salamander"]["password"], password)
        self.assertNotIn(password, output.getvalue())
        self.assertEqual((self.directory / "client-address").read_text(),
                         "hysteria2://8.8.8.8?obfs=salamander\n")
        self.assertEqual(self.directory.stat().st_mode & 0o777, 0o700)
        for path in self.directory.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_ip_certificate_cli_accepts_ipv6_without_email(self):
        result = subprocess.run(
            [sys.executable, str(ROOT / "prepare.py"), "2606:4700:4700::1111",
             "--ip-certificate", "gul-ip", "--output-dir", str(self.directory)],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.directory / "client-address").read_text(),
                         "hysteria2://[2606:4700:4700::1111]\n")

    def test_external_certificate_rejects_nonpublic_ip_and_unsafe_names(self):
        invalid_addresses = (
            "voice.example.org", "8.8.8.8:443", "https://8.8.8.8", "127.0.0.1",
            "10.0.0.1", "169.254.1.1", "100.64.0.1", "192.0.2.1", "224.0.0.1",
            "0.0.0.0", "::1", "fe80::1", "fd00::1", "2001:db8::1", "ff02::1",
            "2606:4700:4700::1111%eth0",
        )
        for address in invalid_addresses:
            with self.subTest(address=address), self.assertRaises(ValueError):
                prepare.prepare(self.directory, address, None, "none",
                                ip_certificate="gul-ip")
            self.assertFalse(self.directory.exists())
        for name in ("../gul-ip", "gul-ip/../../accounts", "other", "", "/gul-ip"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                prepare.prepare(self.directory, "8.8.8.8", None, "none", ip_certificate=name)
            self.assertFalse(self.directory.exists())

    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI is not installed")
    def test_ip_compose_keeps_port_80_free_and_scopes_certificate_keys(self):
        configs = {}
        for filename in ("compose.yaml", "compose.ip.yaml"):
            result = subprocess.run(
                ["docker", "compose", "-f", str(ROOT / filename), "config", "--format", "json"],
                capture_output=True, text=True, check=True,
            )
            configs[filename] = json.loads(result.stdout)
        dns, ip = configs["compose.yaml"], configs["compose.ip.yaml"]
        dns_services, ip_services = dns["services"], ip["services"]
        expected_mumble = {**dns_services["mumble"], "ports": [
            port for port in dns_services["mumble"]["ports"] if port["target"] != 80
        ]}
        self.assertEqual(ip_services["mumble"], expected_mumble)
        mounts = ip_services["hysteria"]["volumes"]
        self.assertEqual({mount["target"] for mount in mounts}, {
            "/etc/hysteria/server.json", "/etc/letsencrypt/live/gul-ip",
            "/etc/letsencrypt/archive/gul-ip",
        })
        for mount in mounts:
            self.assertTrue(mount["read_only"])
            self.assertEqual(mount["type"], "bind")
            if mount["target"].startswith("/etc/letsencrypt/"):
                self.assertEqual(mount["source"],
                                 str(ROOT / "acme" / mount["target"].removeprefix("/etc/letsencrypt/")))
        self.assertEqual(ip_services["hysteria"],
                         {**dns_services["hysteria"], "volumes": mounts})
        self.assertEqual(ip["volumes"], {"mumble-data": dns["volumes"]["mumble-data"]})
        self.assertEqual(ip["secrets"], dns["secrets"])

    @unittest.skipUnless(shutil.which("docker"), "Docker Compose CLI is not installed")
    def test_compose_keeps_mumble_inside_its_own_network_namespace(self):
        self.generate()
        shutil.copy(ROOT / "compose.yaml", self.directory.parent / "compose.yaml")
        result = subprocess.run(
            ["docker", "compose", "-f", str(self.directory.parent / "compose.yaml"),
             "config", "--format", "json"], capture_output=True, text=True, check=True,
        )
        services = json.loads(result.stdout)["services"]
        mumble, hysteria = services["mumble"], services["hysteria"]
        self.assertEqual(hysteria["network_mode"], "service:mumble")
        self.assertEqual(mumble["environment"]["MUMBLE_CONFIG_HOST"], "127.0.0.1")
        self.assertEqual({(port["target"], port["protocol"]) for port in mumble["ports"]},
                         {(80, "tcp"), (443, "tcp"), (443, "udp")})
        self.assertNotIn("ports", hysteria)
        self.assertNotIn("MUMBLE_CONFIG_SERVER_PASSWORD", mumble["environment"])
        self.assertEqual(mumble["image"].split("@", 1)[0], "mumblevoip/mumble-server:v1.5.915")
        self.assertEqual(hysteria["image"].split("@", 1)[0], "tobyxdd/hysteria:v2.13.0")
        self.assertNotIn("NET_ADMIN", hysteria.get("cap_add", []))


if __name__ == "__main__":
    unittest.main()
