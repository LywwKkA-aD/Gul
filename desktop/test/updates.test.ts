import assert from 'node:assert/strict';
import test from 'node:test';
import { checkForUpdate, compareVersions, trustedReleaseURL } from '../src/main/updates.ts';

const release = (tag = 'v0.8.0-alpha.2', extra = {}) => ({
  tag_name: tag,
  draft: false,
  html_url: 'https://malicious.test/installer',
  ...extra,
});
const response = (value: unknown) => async () => Buffer.from(JSON.stringify(value));

test('semantic versions order stable, prerelease and numeric identifiers strictly', () => {
  for (const [older, newer] of [
    ['0.7.9', '0.8.0-alpha.1'],
    ['0.8.0-alpha.2', '0.8.0-alpha.10'],
    ['0.8.0-alpha.1', '0.8.0-beta'],
    ['0.8.0-beta', '0.8.0'],
    ['0.8.0', '0.8.1'],
    ['0.9.0', '1.0.0'],
    ['1.0.0-alpha', '1.0.0-alpha.1'],
    ['1.0.0-1', '1.0.0-alpha'],
  ]) {
    assert.equal(compareVersions(older, newer), -1);
    assert.equal(compareVersions(newer, older), 1);
  }
  assert.equal(compareVersions('v1.2.3+build.1', '1.2.3+build.2'), 0);
  for (const value of [
    'latest',
    '1.2',
    '01.2.3',
    '1.2.3-alpha.01',
    '1.2.3-',
    '1.2.3+',
    '1.2.3\nsecret',
    '1.2.3-<html>',
    '1'.repeat(200),
  ])
    assert.equal(compareVersions(value, '1.2.3'), undefined);
});

test('prerelease checks create a pinned HTTPS GitHub release URL and ignore network-provided URLs', async () => {
  const notice = await checkForUpdate({ current: '0.8.0-alpha.1', request: response([release()]) });
  assert.deepEqual(notice, {
    tag: 'v0.8.0-alpha.2',
    version: '0.8.0-alpha.2',
    url: 'https://github.com/LywwKkA-aD/Gul/releases/tag/v0.8.0-alpha.2',
  });
  assert.equal(trustedReleaseURL(notice!.url), true);
  for (const url of [
    'http://github.com/LywwKkA-aD/Gul/releases/tag/v1.0.0',
    'https://github.com.malicious.test/LywwKkA-aD/Gul/releases/tag/v1.0.0',
    'https://user:password@github.com/LywwKkA-aD/Gul/releases/tag/v1.0.0',
    'https://github.com/LywwKkA-aD/Gul/releases/tag/v1.0.0?token=secret',
    'https://github.com/LywwKkA-aD/Gul/releases/tag/%2e%2e',
    'https://github.com/another/repository/releases/tag/v1.0.0',
  ])
    assert.equal(trustedReleaseURL(url), false);
});

test('downgrades, dismissed versions, invalid tags/drafts/shapes and network failures remain silent', async () => {
  for (const value of [
    [],
    {},
    [release('invalid')],
    [release(undefined, { draft: true })],
    [release('v0.7.0')],
    [release('v0.8.0-alpha.1')],
  ])
    assert.equal(await checkForUpdate({ current: '0.8.0-alpha.1', request: response(value) }), null);
  assert.equal(
    await checkForUpdate({
      current: '0.8.0-alpha.1',
      dismissed: '0.8.0-alpha.2',
      request: response([release()]),
    }),
    null,
  );
  assert.equal(
    (await checkForUpdate({
      current: '0.8.0-alpha.1',
      dismissed: 'invalid',
      request: response([release()]),
    })) !== null,
    true,
  );
  assert.equal(
    await checkForUpdate({
      current: '0.8.0-alpha.1',
      request: async () => {
        throw Error('private network error');
      },
    }),
    null,
  );
  assert.equal(await checkForUpdate({ current: 'invalid', request: response([release()]) }), null);
  assert.equal(
    await checkForUpdate({ current: '0.8.0-alpha.1', request: async () => Buffer.alloc(1048577) }),
    null,
  );
});

test('only exact platform/architecture installer names, owner URLs and SHA-256 digests are offered', async () => {
  const name = 'Gul-0.8.0-alpha.2-win-x64.exe';
  const url = `https://github.com/LywwKkA-aD/Gul/releases/download/v0.8.0-alpha.2/${name}`;
  const asset = {
    name,
    browser_download_url: url,
    size: 100,
    state: 'uploaded',
    digest: `sha256:${'a'.repeat(64)}`,
  };
  const check = (value: unknown) =>
    checkForUpdate({
      current: '0.8.0-alpha.1',
      platform: 'win32',
      arch: 'x64',
      request: response([release(undefined, { assets: [value] })]),
    });
  assert.deepEqual((await check(asset))?.asset, { name, url, size: 100, sha256: 'a'.repeat(64) });
  for (const value of [
    { ...asset, name: 'other.exe' },
    { ...asset, browser_download_url: 'https://malicious.test/installer' },
    { ...asset, digest: 'md5:private' },
    { ...asset, size: 2147483648 },
    { ...asset, state: 'new' },
    { ...asset, digest: undefined },
  ])
    assert.equal((await check(value))?.asset, undefined);
});

test('Linux/mac installer schema and unsupported architecture are handled without inventing downloads', async () => {
  for (const [platform, os, arch, ext] of [
    ['linux', 'linux', 'x64', 'deb'],
    ['darwin', 'mac', 'arm64', 'dmg'],
    ['darwin', 'mac', 'x64', 'dmg'],
  ]) {
    const name = `Gul-0.8.0-alpha.2-${os}-${arch}.${ext}`;
    const asset = {
      name,
      browser_download_url: `https://github.com/LywwKkA-aD/Gul/releases/download/v0.8.0-alpha.2/${name}`,
      size: 200,
      state: 'uploaded',
      digest: `sha256:${'b'.repeat(64)}`,
    };
    const notice = await checkForUpdate({
      current: '0.8.0-alpha.1',
      platform,
      arch,
      request: response([release(undefined, { assets: [asset] })]),
    });
    assert.equal(notice?.asset?.name, name);
  }
  assert.equal(
    (
      await checkForUpdate({
        current: '0.8.0-alpha.1',
        platform: 'linux',
        arch: 'ia32',
        request: response([release(undefined, { assets: [] })]),
      })
    )?.asset,
    undefined,
  );
});
