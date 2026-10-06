#!/usr/bin/env python3
"""Exercise local-lab startup without a daemon, network, or real credentials."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SOURCE = Path(__file__).with_name('livekit-local.sh')
PRIVATE_LOG = 'synthetic-secret-must-stay-in-private-log'


class LocalLiveKitTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        shutil.copyfile(SOURCE, self.root / 'scripts/livekit-local.sh')
        (self.root / 'frontend/dist').mkdir(parents=True)
        (self.root / 'frontend/dist/index.html').write_text('<!doctype html>')
        self.shims = self.root / 'shims'
        self.shims.mkdir()
        self.env = {**os.environ, 'PATH': str(self.shims) + ':' + os.environ['PATH'],
                    'LAB_TEST_ROOT': str(self.root), 'LAB_TEST_STATE': 'running'}
        self.executable('id', '#!/bin/sh\nprintf "1001\\n"\n')
        self.executable('curl', '#!/bin/sh\n[ "$LAB_TEST_STATE" = running ]\n')
        self.executable('sleep', '#!/bin/sh\nexit 0\n')
        self.executable('go', '''#!/usr/bin/env python3
import os, pathlib, sys
target = pathlib.Path(sys.argv[sys.argv.index('-o') + 1])
target.write_text(''' + repr('''#!/usr/bin/env python3
import pathlib, sys
if sys.argv[1] == '-init':
    path = pathlib.Path(sys.argv[2])
    (path / 'server.yaml').write_text('synthetic config')
    (path / 'broker.json').write_text('{}')
''') + ''')
target.chmod(0o700)
''')
        self.executable('docker', '''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
root = pathlib.Path(os.environ['LAB_TEST_ROOT'])
with (root / 'docker.calls').open('a') as out:
    out.write(json.dumps(args) + '\\n')
if args[0] == 'inspect':
    if '.Config.Labels' in args[2]:
        sys.exit(1)
    state = os.environ['LAB_TEST_STATE']
    print(state + (' 1 false' if '.State.ExitCode' in args[2] else ''))
elif args[0] == 'logs':
    print(''' + repr(PRIVATE_LOG) + ''')
''')

    def executable(self, name, body):
        path = self.shims / name
        path.write_text(body)
        path.chmod(0o700)

    def run_lab(self, state='running'):
        return subprocess.run(['bash', str(self.root / 'scripts/livekit-local.sh'), 'up', '--foreground'],
                              env={**self.env, 'LAB_TEST_STATE': state},
                              capture_output=True, text=True, timeout=15)

    def test_runs_as_config_owner_without_weakening_container_or_file_permissions(self):
        result = self.run_lab()
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = [json.loads(line) for line in (self.root / 'docker.calls').read_text().splitlines()]
        run, = [args for args in calls if args[0] == 'run']
        self.assertIn('--user', run)
        self.assertEqual(run[run.index('--user') + 1], '1001:1001')
        self.assertEqual(run[run.index('--cap-drop') + 1], 'ALL')
        self.assertIn('--read-only', run)
        self.assertIn('no-new-privileges:true', run)
        self.assertNotIn('--privileged', run)
        self.assertEqual((self.root / 'bin/livekit-local/server.yaml').stat().st_mode & 0o777, 0o600)

    def test_startup_failure_retains_private_logs_and_reports_only_safe_state(self):
        result = self.run_lab('exited')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(PRIVATE_LOG, result.stdout + result.stderr)
        self.assertIn('exited', result.stderr)
        log = self.root / 'bin/livekit-local/server.log'
        self.assertIn(PRIVATE_LOG, log.read_text())
        self.assertEqual(log.stat().st_mode & 0o777, 0o600)
        calls = [json.loads(line) for line in (self.root / 'docker.calls').read_text().splitlines()]
        self.assertEqual(sum(args[0] == 'logs' for args in calls), 1)
        polls = [args for args in calls if args[:3] == ['inspect', '--format', '{{.State.Status}}']]
        self.assertEqual(len(polls), 1, 'an exited SFU must fail before the readiness timeout')


if __name__ == '__main__':
    unittest.main()
