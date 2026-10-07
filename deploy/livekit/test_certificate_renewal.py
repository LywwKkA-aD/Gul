import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class CertificateRenewalTests(unittest.TestCase):
    def run_renewal(self, arguments, docker_status=0):
        with tempfile.TemporaryDirectory(prefix='gul-renew-test-') as directory:
            root = Path(directory)
            marker = root / 'updated'
            docker = root / 'docker'
            docker.write_text(f'#!/bin/sh\nexit {docker_status}\n')
            docker.chmod(0o700)
            update = root / 'update'
            update.write_text(f'#!/bin/sh\ntouch "{marker}"\n')
            update.chmod(0o700)
            source = Path(__file__).with_name('renew-certificate.sh').read_text()
            source = source.replace('/usr/bin/docker', str(docker)).replace('/opt/gul-livekit/update-certificate.sh', str(update))
            script = root / 'renew'
            script.write_text(source)
            script.chmod(0o700)
            result = subprocess.run([str(script), *arguments], capture_output=True, env={'PATH': os.environ['PATH']})
            return result.returncode, marker.exists()

    def test_dry_run_never_updates_running_services(self):
        self.assertEqual(self.run_renewal(['--dry-run']), (0, False))
        self.assertEqual(self.run_renewal(['--force-renewal', '--dry-run']), (0, False))

    def test_successful_actual_renewal_updates_certificate(self):
        self.assertEqual(self.run_renewal([]), (0, True))

    def test_failed_renewal_never_updates_running_services(self):
        self.assertEqual(self.run_renewal([], docker_status=1), (1, False))


if __name__ == '__main__':
    unittest.main()
