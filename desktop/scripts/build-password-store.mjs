import { spawnSync } from 'node:child_process';
import { mkdir, chmod, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const desktop = dirname(dirname(fileURLToPath(import.meta.url)));
if (process.platform !== 'linux') {
  console.info('Native password storage helper is Linux-only.');
} else {
  const directory = join(desktop, 'resources', 'password-store', `linux-${process.arch}`);
  await mkdir(directory, { recursive: true });
  const flags = spawnSync('/usr/bin/pkg-config', ['--cflags', '--libs', 'gio-2.0'], { encoding: 'utf8' });
  if (flags.status !== 0) throw Error('GUL_PASSWORD_STORE_BUILD_DEPENDENCIES');
  const compile = (source, destination) => {
    const result = spawnSync(
      '/usr/bin/gcc',
      [
        '-std=c11',
        '-D_POSIX_C_SOURCE=200809L',
        '-O2',
        '-Wall',
        '-Wextra',
        '-Werror',
        '-Wformat=2',
        '-fstack-protector-strong',
        '-D_FORTIFY_SOURCE=2',
        '-fPIE',
        '-pie',
        '-Wl,-z,relro,-z,now',
        source,
        '-o',
        destination,
        ...flags.stdout.trim().split(/\s+/u),
      ],
      { stdio: 'inherit' },
    );
    if (result.status !== 0) throw Error('GUL_PASSWORD_STORE_BUILD_FAILED');
  };
  const executable = join(directory, 'gul-password-store');
  compile(join(desktop, 'native', 'password-store', 'linux.c'), executable);
  await chmod(executable, 0o755);
  const invalid = spawnSync(executable, [], { stdio: 'ignore' });
  if (invalid.status !== 64) throw Error('GUL_PASSWORD_STORE_ARGUMENT_PROOF_FAILED');
  const privateTests = await mkdtemp(join(tmpdir(), 'gul-password-store-proof-'));
  try {
    const testService = join(privateTests, 'gul-password-store-test-service');
    compile(join(desktop, 'native', 'password-store', 'test-service.c'), testService);
    const proof = spawnSync(
      '/usr/bin/dbus-run-session',
      [
        '--',
        '/usr/bin/python3',
        join(desktop, 'scripts', 'password-store-native-proof.py'),
        executable,
        testService,
      ],
      { stdio: 'inherit', timeout: 30_000 },
    );
    if (proof.status !== 0) throw Error('GUL_PASSWORD_STORE_NATIVE_PROOF_FAILED');
  } finally {
    await rm(privateTests, { recursive: true, force: true });
  }
  console.info(
    'Native password storage helper built; metadata, single-connection prompt and cancellation proofs passed.',
  );
}
