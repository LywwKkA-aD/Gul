import hashlib
import importlib.util
import ipaddress
import json
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location('livekit_prepare', Path(__file__).with_name('prepare.py'))
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


class PrepareTests(unittest.TestCase):
    def test_credentials_are_private_and_not_in_proxy(self):
        password = 'synthetic-test-password-with-entropy'
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / 'private'
            prepare.generate('203.0.113.10', password, out)
            broker = json.loads((out / 'broker.json').read_text())
            sfu = json.loads((out / 'livekit.yaml').read_text())
            proxy = (out / 'haproxy.cfg').read_text()
            self.assertEqual(broker['joinPasswordSHA256'], hashlib.sha256(password.encode()).hexdigest())
            self.assertEqual(broker['publicOrigin'], 'https://203.0.113.10')
            self.assertEqual(broker['liveKitURL'], 'wss://203.0.113.10')
            self.assertEqual(sfu['keys'], {broker['apiKey']: broker['apiSecret']})
            self.assertNotIn(password, proxy)
            self.assertNotIn(broker['apiSecret'], proxy)
            self.assertEqual(out.stat().st_mode & 0o777, 0o700)
            for path in out.iterdir():
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                prepare.generate('203.0.113.10', password, out)

    def test_tls_turn_only_and_no_arbitrary_relay_destination(self):
        config = prepare.sfu_config('203.0.113.10', 'synthetic-key', 's' * 64)
        turn = config['turn']
        self.assertEqual(turn['udp_port'], 0)
        self.assertTrue(turn['external_tls'])
        self.assertTrue(turn['proxy_protocol'])
        self.assertEqual(turn['proxy_protocol_trusted_cidrs'], ['127.0.0.0/8'])
        denied = [ipaddress.ip_network(cidr) for cidr in turn['deny_peer_cidrs']]
        for address, blocked in [('203.0.113.10', False), ('203.0.113.11', True),
                                 ('1.1.1.1', True), ('127.0.0.1', True), ('10.0.0.1', True), ('::1', True)]:
            ip = ipaddress.ip_address(address)
            self.assertEqual(any(ip in network for network in denied), blocked)

    def test_proxy_does_not_expose_admin_or_trust_forwarded_input(self):
        proxy = prepare.proxy_config('203.0.113.10')
        self.assertIn('bind 0.0.0.0:443 ssl', proxy)
        self.assertIn('alpn http/1.1', proxy)
        self.assertIn('http-request set-header X-Forwarded-For %[src]', proxy)
        self.assertIn('http-request set-header X-Forwarded-Proto https', proxy)
        self.assertIn('127.0.0.1:5349 send-proxy-v2', proxy)
        self.assertIn('http-request deny deny_status 404 unless gul_api or rtc or health or root', proxy)
        self.assertNotIn('option httplog', proxy)
        self.assertNotIn('/twirp', proxy)

    def test_bad_inputs_create_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            for address, password in [('example.org', 'x' * 32), ('127.0.0.1', 'x' * 32),
                                      ('203.0.113.10\nfrontend bad', 'x' * 32),
                                      ('203.0.113.10', 'short'), ('203.0.113.10', 'x' * 32 + '\n')]:
                out = Path(tmp) / 'private'
                with self.assertRaises(ValueError):
                    prepare.generate(address, password, out)
                self.assertFalse(out.exists())


if __name__ == '__main__':
    unittest.main()
