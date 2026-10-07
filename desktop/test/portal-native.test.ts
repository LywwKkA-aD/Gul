import { execFileSync } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';
import { linuxCompileArguments } from '../scripts/build-ptt.mjs';

test(
  'native Linux portal binds an owner-scoped session, emits actual release and fails closed',
  { skip: process.platform !== 'linux', timeout: 30000 },
  async () => {
    const temporary = await mkdtemp(join(tmpdir(), 'gul-portal-test-'));
    try {
      const flags = execFileSync('pkg-config', ['--cflags', '--libs', 'gio-2.0'], { encoding: 'utf8' })
        .trim()
        .split(/\s+/u);
      const helper = join(temporary, 'gul-ptt'),
        fixture = join(temporary, 'portal-fixture');
      for (const [source, output] of [
        ['linux.cpp', helper],
        ['linux.test.cpp', fixture],
      ]) {
        execFileSync('g++', linuxCompileArguments(resolve('native/ptt', source), output, flags), {
          stdio: 'inherit',
          timeout: 15000,
        });
      }
      execFileSync(fixture, [helper], { stdio: 'inherit', timeout: 20000 });
    } finally {
      await rm(temporary, { recursive: true, force: true });
    }
  },
);
