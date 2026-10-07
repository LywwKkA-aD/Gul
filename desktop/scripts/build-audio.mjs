import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { buildWindowsAudio } from './build-windows-audio.mjs';

export function audioCompileArguments(source, output, pulseFlags) {
  return [
    '-std=c++17',
    '-O2',
    '-Wall',
    '-Wextra',
    '-Werror',
    '-fstack-protector-strong',
    '-D_FORTIFY_SOURCE=2',
    '-fPIE',
    '-pie',
    '-Wl,-z,relro,-z,now',
    source,
    '-o',
    output,
    ...pulseFlags,
  ];
}

/** Audio PCM stays in the local audio server; the helper's only stdout protocol is READY/ERROR. */
export async function buildLinuxAudio() {
  if (process.platform !== 'linux') return undefined;
  if (process.arch !== 'x64') throw new Error('Linux audio capture requires an x64 build host');
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const source = join(root, 'native', 'audio-capture');
  const output = join(root, 'resources', 'audio-capture', 'linux-x64', 'gul-audio');
  const temporary = await mkdtemp(join(tmpdir(), 'gul-audio-build-'));
  try {
    const policy = join(temporary, 'audio-policy-test');
    execFileSync('g++', audioCompileArguments(join(source, 'policy.test.cpp'), policy, []), {
      stdio: 'inherit',
    });
    execFileSync(policy, [], { stdio: 'inherit' });
    const flags = execFileSync('pkg-config', ['--cflags', '--libs', 'libpulse'], {
      encoding: 'utf8',
      maxBuffer: 64 * 1024,
    })
      .trim()
      .split(/\s+/u);
    await mkdir(dirname(output), { recursive: true });
    execFileSync('g++', audioCompileArguments(join(source, 'linux.cpp'), output, flags), {
      stdio: 'inherit',
    });
    return output;
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const output = process.platform === 'win32' ? await buildWindowsAudio() : await buildLinuxAudio();
    process.stdout.write(
      output
        ? 'Native audio helper built and policy tests passed.\n'
        : 'Native audio helpers are built on Windows or Linux only.\n',
    );
  } catch {
    process.stderr.write('Unable to build the native audio helper.\n');
    process.exitCode = 1;
  }
}
