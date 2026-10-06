import base64
import importlib.util
import ipaddress
import json
from pathlib import Path
import tempfile
import unittest
from urllib.parse import parse_qs, urlparse


ROOT = Path(__file__).parent
spec = importlib.util.spec_from_file_location('livekit_reality', ROOT / 'prepare_reality.py')
reality = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reality)
stand_spec = importlib.util.spec_from_file_location('livekit_reality_stand', ROOT / 'stand_reality.py')
stand = importlib.util.module_from_spec(stand_spec)
stand_spec.loader.exec_module(stand)


class RealityTests(unittest.TestCase):
    def test_local_fixture_turn_allows_only_its_private_sfu_ip(self):
        node_ip = '192.168.215.5'
        config = stand.fixture_sfu_config(node_ip, 'synthetic-key', 'synthetic-secret')
        self.assertEqual(config['turn']['allow_restricted_peer_cidrs'], [node_ip + '/32'])
        denied = [ipaddress.ip_network(cidr) for cidr in config['turn']['deny_peer_cidrs']]
        self.assertFalse(any(ipaddress.ip_address(node_ip) in cidr for cidr in denied))
        for blocked in ['127.0.0.1', '192.168.215.1', '192.168.215.6', '10.0.0.1']:
            self.assertTrue(any(ipaddress.ip_address(blocked) in cidr for cidr in denied))

    def test_user_id_matches_embedded_client(self):
        self.assertEqual(reality.user_id('gul-test-password'), '0b70bd5c-254d-8d93-8a14-98359d1ad0fb')
        self.assertNotEqual(reality.user_id('password'), reality.user_id(' password'))

    def test_only_authenticated_tcp_to_local_tls_is_routed(self):
        config = reality.server_config('synthetic-password', 'www.example.org', 'A' * 43, '0123456789abcdef')
        inbound, = config['inbounds']
        self.assertEqual((inbound['listen'], inbound['port']), ('127.0.0.1', 8443))
        self.assertEqual(inbound['settings']['clients'], [{'id': reality.user_id('synthetic-password'), 'flow': ''}])
        settings = inbound['streamSettings']['realitySettings']
        self.assertEqual(settings['target'], 'www.example.org:443')
        self.assertEqual(settings['serverNames'], ['www.example.org'])
        self.assertEqual(config['outbounds'][0]['protocol'], 'blackhole')
        self.assertEqual(config['outbounds'][1]['settings']['redirect'], '127.0.0.1:443')
        allowed, denied = config['routing']['rules']
        self.assertEqual(allowed['network'], 'tcp')
        self.assertEqual(allowed['ip'], ['127.0.0.1/32'])
        self.assertEqual(allowed['port'], '443')
        self.assertEqual(allowed['outboundTag'], 'gul-tls')
        self.assertEqual(denied['outboundTag'], 'blocked')
        self.assertEqual(config['log'], {'loglevel': 'error', 'access': 'none'})

    def test_public_profile_contains_no_authentication_password_or_uuid(self):
        profile = reality.address('203.0.113.9', 'www.example.org', 'A' * 43, 'abcd')
        parsed = urlparse(profile)
        self.assertEqual(parsed.scheme, 'livekit+vless')
        self.assertEqual(parsed.netloc, '203.0.113.9')
        self.assertIsNone(parsed.username)
        self.assertEqual(set(parse_qs(parsed.query)), {'flow', 'pbk', 'security', 'sid', 'sni', 'type'})
        self.assertEqual(parse_qs(parsed.query)['flow'], ['none'])
        self.assertEqual(len(base64.urlsafe_b64decode(parse_qs(parsed.query)['pbk'][0] + '=')), 32)

    def test_generated_files_are_private_and_do_not_replace_existing_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / 'reality'
            reality.generate('203.0.113.9', 'synthetic-password', 'www.example.org', 'A' * 43, 'B' * 42 + 'A', 'abcd', out)
            self.assertEqual(out.stat().st_mode & 0o777, 0o700)
            self.assertEqual(set(path.name for path in out.iterdir()),
                             {'server.json', 'address', 'haproxy.cfg', 'Gul-LiveKit-Reality-server.txt'})
            for path in out.iterdir():
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            config = json.loads((out / 'server.json').read_text())
            self.assertEqual(config['inbounds'][0]['port'], 8443)
            proxy = (out / 'haproxy.cfg').read_text()
            self.assertNotIn('synthetic-password', proxy)
            self.assertNotIn('A' * 43, proxy)
            with self.assertRaises(FileExistsError):
                reality.generate('203.0.113.9', 'synthetic-password', 'www.example.org', 'A' * 43, 'B' * 42 + 'A', 'abcd', out)

    def test_bad_inputs_create_nothing(self):
        defaults = ('203.0.113.9', 'synthetic-password', 'www.example.org', 'A' * 43, 'A' * 43, 'abcd')
        bad = [(0, '127.0.0.1'), (1, 'short'), (1, 'x' * 32 + '\n'),
               (2, 'www.example.org\nbackend evil'), (2, '127.0.0.1'),
               (3, 'bad'), (4, 'A' * 42 + 'B'), (5, 'ABCDEF'), (5, '')]
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / 'private'
            for index, value in bad:
                args = list(defaults)
                args[index] = value
                with self.subTest(index=index), self.assertRaises(ValueError):
                    reality.generate(*args, out)
                self.assertFalse(out.exists())

    def test_mux_preserves_https_and_turn_and_pins_reality_sni(self):
        proxy = reality.proxy_config('203.0.113.9', 'www.example.org')
        self.assertIn('frontend gul_ingress\n    bind 0.0.0.0:443', proxy)
        self.assertIn('acl reality_sni req.ssl_sni -i www.example.org', proxy)
        self.assertIn('use_backend gul_reality_transport if reality_sni', proxy)
        self.assertIn('default_backend gul_tls_transport', proxy)
        self.assertIn('server local_reality 127.0.0.1:8443', proxy)
        self.assertIn('server local_tls 127.0.0.1:9443 send-proxy-v2', proxy)
        self.assertIn('bind 127.0.0.1:9443 accept-proxy ssl crt /etc/haproxy/gul.pem alpn http/1.1', proxy)
        self.assertIn('server local_turn 127.0.0.1:5349 send-proxy-v2', proxy)
        self.assertIn('acl rtc path /rtc /rtc/validate /rtc/v1 /rtc/v1/validate', proxy)
        self.assertNotIn('option httplog', proxy)
        with self.assertRaises(ValueError):
            reality.proxy_config('203.0.113.9', 'bad\nbackend evil')


if __name__ == '__main__':
    unittest.main()
