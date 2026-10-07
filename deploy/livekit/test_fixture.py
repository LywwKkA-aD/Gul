import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch


spec = importlib.util.spec_from_file_location('isolated_livekit_fixture', Path(__file__).with_name('stand_reality.py'))
stand = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stand)


class FixtureTests(unittest.TestCase):
    def test_failed_command_redacts_all_diagnostics(self):
        result = subprocess.CompletedProcess(['docker'], 1, 'synthetic-private-token', 'synthetic-private-password')
        with patch.object(stand.subprocess, 'run', return_value=result):
            with self.assertRaisesRegex(RuntimeError, '^fixture command failed: docker$'):
                stand.command('docker', 'inspect', 'synthetic-container')

    def test_command_returns_only_successful_output(self):
        result = subprocess.CompletedProcess(['docker'], 0, '  ready\n', '')
        with patch.object(stand.subprocess, 'run', return_value=result) as run:
            self.assertEqual(stand.command('docker', 'version'), 'ready')
            run.assert_called_once_with(('docker', 'version'), capture_output=True, text=True)

    def test_private_write_never_overwrites_existing_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'password'
            stand.write(path, 'synthetic-private-password')
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                stand.write(path, 'replacement')
            self.assertEqual(path.read_text(), 'synthetic-private-password')

    def test_existing_fixture_is_not_replaced_or_started(self):
        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(stand, 'command') as command:
                with self.assertRaises(FileExistsError):
                    stand.start(Path(tmp), Path('unused-xray'), Path('unused-broker'), 'unused-image')
                command.assert_not_called()

    def test_cleanup_is_limited_to_recorded_fixture_containers(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            names = ['gul-reality-test-synthetic-sfu', 'gul-reality-test-synthetic-gateway']
            (output / 'containers.json').write_text(json.dumps(names))
            password = output / 'join-password'
            password.write_text('synthetic-private-password')
            with patch.object(stand.subprocess, 'run') as run, redirect_stdout(io.StringIO()) as logs:
                stand.remove(output)
                self.assertEqual(run.call_count, 2)
                self.assertEqual([call.args[0] for call in run.call_args_list],
                                 [['docker', 'rm', '-f', name] for name in names])
                self.assertNotIn('synthetic-private-password', logs.getvalue())
            self.assertTrue(password.is_file())

    def test_cleanup_rejects_unrelated_container(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            (output / 'containers.json').write_text(json.dumps(['production-server']))
            with patch.object(stand.subprocess, 'run') as run:
                with self.assertRaisesRegex(ValueError, 'unexpected fixture container name'):
                    stand.remove(output)
                run.assert_not_called()


if __name__ == '__main__':
    unittest.main()
