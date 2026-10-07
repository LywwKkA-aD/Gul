import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { collectArtifacts, validateReleaseTag } from '../scripts/release-assets.mjs';

test('release tags must match the single client version', () => {
  assert.doesNotThrow(() => validateReleaseTag('v0.8.0-alpha.1', '0.8.0-alpha.1'));
  assert.throws(() => validateReleaseTag('v0.7.0-alpha.6', '0.8.0-alpha.1'));
  assert.throws(() => validateReleaseTag('v../other', '../other'));
});

test('release collection includes only this version and creates verifiable hashes', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-release-'));
  try {
    const name = 'Gul-0.8.0-alpha.1-linux-x64.deb';
    await writeFile(join(directory, name), 'installer fixture');
    await writeFile(join(directory, 'Gul-0.8.0-alpha.0-linux-x64.deb'), 'old installer');
    const assets = await collectArtifacts(directory, '0.8.0-alpha.1', 'linux', 'x64');
    assert.deepEqual(assets, [name]);
    assert.equal(await readFile(join(directory, 'release', name), 'utf8'), 'installer fixture');
    assert.match(
      await readFile(join(directory, 'release', name + '.sha256'), 'utf8'),
      /^[a-f0-9]{64}  Gul-0\.8\.0-alpha\.1-linux-x64\.deb\n$/u,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('missing Windows installer and linked installer fail release collection', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-release-'));
  try {
    await writeFile(join(directory, 'Gul-0.8.0-alpha.1-win-x64.zip'), 'zip fixture');
    await assert.rejects(collectArtifacts(directory, '0.8.0-alpha.1', 'win32', 'x64'));
    if (process.platform !== 'win32') {
      await symlink(
        join(directory, 'Gul-0.8.0-alpha.1-win-x64.zip'),
        join(directory, 'Gul-0.8.0-alpha.1-win-x64.exe'),
      );
      await assert.rejects(collectArtifacts(directory, '0.8.0-alpha.1', 'win32', 'x64'));
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
