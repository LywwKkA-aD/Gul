import { spawnSync } from 'node:child_process';

const python = process.platform === 'win32' ? 'python' : 'python3';
const result = spawnSync(python, ['scripts/fetch-xray.py', ...process.argv.slice(2)], { stdio: 'inherit' });
if (result.error) process.stderr.write('Python 3 is required to download the verified Xray binary.\n');
process.exitCode = result.status ?? 1;
