import { readdir } from 'node:fs/promises';
import { spawnSync } from 'node:child_process';

const tests = (await readdir('test'))
  .filter((name) => name.endsWith('.test.ts'))
  .sort()
  .map((name) => `test/${name}`);
const coverage = process.argv.includes('--coverage')
  ? [
      '--experimental-test-coverage',
      '--test-coverage-lines=80',
      '--test-coverage-branches=80',
      '--test-coverage-functions=80',
    ]
  : [];
const result = spawnSync(process.execPath, [...coverage, '--test', ...tests], { stdio: 'inherit' });
process.exitCode = result.status ?? 1;
