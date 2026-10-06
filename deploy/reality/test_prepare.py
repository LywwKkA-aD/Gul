import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
import uuid

spec = importlib.util.spec_from_file_location('prepare_reality', Path(__file__).with_name('prepare.py'))
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


class PrepareTests(unittest.TestCase):
    def test_credential_is_stable_domain_separated_uuid(self):
        value = prepare.user_id('test-password')
        digest = bytearray(hashlib.sha256(b'gul/vless-reality/user-id/v1\0test-password').digest()[:16])
        digest[6] = (digest[6] & 15) | 128
        digest[8] = (digest[8] & 63) | 128
        self.assertEqual(value, str(uuid.UUID(bytes=bytes(digest))))
        self.assertEqual(uuid.UUID(value).version, 8)
        self.assertNotEqual(value, prepare.user_id('test-password '))

    def test_server_only_allows_mumble_tcp(self):
        config = prepare.server_config('test-password', 'www.example.com', 'private-key', '0123456789abcdef')
        inbound = config['inbounds'][0]
        self.assertEqual(inbound['port'], 443)
        self.assertEqual(inbound['streamSettings']['security'], 'reality')
        self.assertEqual(inbound['settings']['clients'][0]['flow'], '')
        self.assertEqual(config['outbounds'][0]['protocol'], 'blackhole')
        direct = config['outbounds'][1]
        self.assertEqual(direct['settings']['redirect'], '127.0.0.1:64738')
        allowed, denied = config['routing']['rules']
        self.assertEqual(allowed['ip'], ['127.0.0.1/32'])
        self.assertEqual(allowed['port'], '64738')
        self.assertEqual(allowed['network'], 'tcp')
        self.assertEqual(allowed['outboundTag'], direct['tag'])
        self.assertEqual(denied['outboundTag'], 'blocked')

    def test_profile_has_no_user_password_or_uuid(self):
        address = prepare.address('203.0.113.7', 'www.example.com', 'A'*43, '0123456789abcdef')
        self.assertTrue(address.startswith('vless://203.0.113.7?'))
        self.assertNotIn('@', address)
        self.assertIn('security=reality', address)
        self.assertIn('flow=none', address)

    def test_rejects_invalid_endpoint_metadata(self):
        for host, sni in [('x@evil.test', 'example.com'), ('203.0.113.7', 'bad/site'), ('203.0.113.7', '127.0.0.1')]:
            with self.subTest(host=host, sni=sni), self.assertRaises(ValueError):
                prepare.address(host, sni, 'A'*43, 'abcd')

    def test_secret_output_never_overwrites_existing(self):
        with tempfile.TemporaryDirectory() as root:
            target = Path(root)/'secret'
            prepare.write_private(target, 'first')
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                prepare.write_private(target, 'second')
            self.assertEqual(target.read_text(), 'first')

    def test_key_parser_accepts_pinned_release_labels(self):
        private, public = prepare.parse_keys('PrivateKey: secret\nPassword (PublicKey): public\nHash32: unused\n')
        self.assertEqual((private, public), ('secret', 'public'))
        with self.assertRaises(ValueError):
            prepare.parse_keys('unexpected output')


if __name__ == '__main__':
    unittest.main()
