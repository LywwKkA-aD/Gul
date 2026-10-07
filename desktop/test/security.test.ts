import test from 'node:test';
import assert from 'node:assert/strict';
import {
  appAsset,
  appPage,
  captureAllowed,
  displayMediaPreflight,
  appOrigin,
  mediaPermission,
  allowedNetwork,
  contentSecurityPolicy,
  testOptions,
} from '../src/main/security.ts';

test('only the registered app document is trusted; subframes and lookalike URLs are denied', () => {
  assert.equal(appPage('gul://app/index.html'), true);
  assert.equal(appPage('gul://app/'), true);
  for (const url of [
    'https://app/index.html',
    'gul://app.evil/index.html',
    'gul://user@app/index.html',
    'gul://app:12/index.html',
    'gul://app/other.html',
    'gul://app/index.html?token=x',
    'file:///index.html',
    'bad',
  ])
    assert.equal(appPage(url), false, url);
  const request = { securityOrigin: 'gul://app', videoRequested: true, userGesture: true };
  assert.equal(captureAllowed(request, 'gul://app/index.html', true), true);
  for (const change of [
    { userGesture: false },
    { videoRequested: false },
    { securityOrigin: 'https://evil.example' },
  ])
    assert.equal(captureAllowed({ ...request, ...change }, 'gul://app/index.html', true), false);
  assert.equal(captureAllowed(request, 'gul://app/index.html', false), false);
  assert.equal(captureAllowed(request, 'https://evil.example', true), false);
});

test('serialized app origins accept the bare origin and its canonical root URL only', () => {
  for (const origin of ['gul://app', 'gul://app/']) {
    assert.equal(appOrigin(origin), true);
    assert.equal(
      captureAllowed(
        { securityOrigin: origin, videoRequested: true, userGesture: true },
        'gul://app/index.html',
        true,
      ),
      true,
    );
  }
  for (const origin of [
    'gul://app/index.html',
    'gul://app?token=secret',
    'gul://app/#fragment',
    'gul://app:443/',
    'gul://user@app/',
    'gul://app.evil/',
    'https://app/',
    undefined,
  ])
    assert.equal(appOrigin(origin), false);
});

test('Electron display preflight requires explicit empty device types, the owned app frame and an active session', () => {
  const details = {
    securityOrigin: 'gul://app/',
    requestingUrl: 'gul://app/index.html',
    isMainFrame: true,
    mediaTypes: [] as readonly string[],
  };
  assert.equal(displayMediaPreflight('media', details, true, true, true), true);
  assert.equal(
    displayMediaPreflight('media', { ...details, securityOrigin: 'gul://app' }, true, true, true),
    true,
  );
  for (const change of [
    { mediaTypes: undefined },
    { mediaTypes: ['audio'] },
    { mediaTypes: ['video'] },
    { mediaTypes: ['audio', 'video'] },
    { mediaType: 'video' },
    { mediaType: 'audio' },
    { securityOrigin: undefined },
    { securityOrigin: 'gul://app/index.html' },
    { securityOrigin: 'https://evil.example/' },
    { requestingUrl: 'https://evil.example/' },
    { requestingUrl: undefined },
    { isMainFrame: false },
  ])
    assert.equal(displayMediaPreflight('media', { ...details, ...change }, true, true, true), false);
  assert.equal(displayMediaPreflight('media', details, false, true, true), false);
  assert.equal(displayMediaPreflight('media', details, true, false, true), false);
  assert.equal(displayMediaPreflight('media', details, true, true, false), false);
  assert.equal(displayMediaPreflight('display-capture', details, true, true, true), false);
  assert.equal(
    displayMediaPreflight('media', { ...details, mediaTypes: 'empty' } as never, true, true, true),
    false,
  );
});

test('asset serving never resolves traversal, foreign origins, or unbundled resources', () => {
  assert.equal(appAsset('gul://app/'), 'index.html');
  assert.equal(appAsset('gul://app/renderer.js'), 'renderer.js');
  assert.equal(appAsset('gul://app/assets/font.woff2'), 'assets/font.woff2');
  for (const url of [
    'gul://app/%2e%2e/private',
    'gul://app/%2fetc/passwd',
    'gul://app/a%5cb.js',
    'gul://evil/renderer.js',
    'gul://app/package.json',
    'gul://app/other.html',
    'gul://app/a.js?x=1',
    'gul://app/a..b.js',
  ])
    assert.equal(appAsset(url), null, url);
});

test('microphone permissions are restricted to app main frame and audio only', () => {
  assert.equal(
    mediaPermission(
      'media',
      { requestingUrl: 'gul://app/index.html', isMainFrame: true, mediaTypes: ['audio'] },
      true,
    ),
    true,
  );
  assert.equal(
    mediaPermission(
      'media',
      { requestingUrl: 'gul://app/index.html', isMainFrame: true, mediaType: 'audio' },
      true,
    ),
    true,
  );
  for (const value of [
    { mediaTypes: ['video'] },
    { mediaTypes: ['audio', 'video'] },
    { mediaTypes: [] },
    { mediaType: 'unknown' },
    { mediaTypes: ['audio'], isMainFrame: false },
    { mediaTypes: ['audio'], requestingUrl: 'https://evil.example' },
  ]) {
    assert.equal(
      mediaPermission('media', { requestingUrl: 'gul://app/index.html', isMainFrame: true, ...value }, true),
      false,
    );
  }
  assert.equal(
    mediaPermission(
      'media',
      { requestingUrl: 'gul://app/index.html', isMainFrame: true, mediaTypes: ['audio'] },
      false,
    ),
    false,
  );
  assert.equal(
    mediaPermission('geolocation', { requestingUrl: 'gul://app/index.html', isMainFrame: true }, true),
    false,
  );
});

test('renderer networking is limited to session capability paths on the owned gateway', () => {
  const endpoint = 'ws://127.0.0.1:9911/secretcapability';
  for (const tail of ['/rtc', '/rtc/v1', '/rtc/validate', '/rtc/v1/validate'])
    assert.equal(
      allowedNetwork(`http://127.0.0.1:9911/secretcapability${tail}?access_token=x`, [endpoint]),
      true,
    );
  assert.equal(allowedNetwork('ws://127.0.0.1:9911/secretcapability/rtc', [endpoint]), true);
  for (const url of [
    'https://public.example/rtc',
    'http://127.0.0.1:9912/secretcapability/rtc',
    'http://127.0.0.1:9911/api/gul/login',
    'http://localhost:9911/secretcapability/rtc',
    'http://127.0.0.1:9911/other/rtc',
    'file:///etc/passwd',
    'invalid',
  ])
    assert.equal(allowedNetwork(url, [endpoint]), false, url);
  assert.equal(allowedNetwork('gul://app/renderer.js', []), true);
  assert.equal(allowedNetwork('gul://other/renderer.js', []), false);
  assert.equal(contentSecurityPolicy.includes("script-src 'self'"), true);
  assert.equal(contentSecurityPolicy.includes("object-src 'none'"), true);
  assert.match(contentSecurityPolicy, /script-src 'self' 'wasm-unsafe-eval'(?:;|$)/u);
  assert.equal(contentSecurityPolicy.includes("'unsafe-eval'"), false);
  assert.doesNotMatch(contentSecurityPolicy, /script-src[^;]*'unsafe-inline'/u);
});

test('test CA and executable overrides require an unpackaged, explicit test launch', () => {
  const env = {
    NODE_ENV: 'test',
    GUL_ELECTRON_TEST_CA: '/private/fixture.pem',
    GUL_ELECTRON_TEST_XRAY: '/private/xray',
  };
  assert.deepEqual(testOptions(env, false, ['--gul-electron-test']), {
    caFile: '/private/fixture.pem',
    xrayPath: '/private/xray',
  });
  assert.deepEqual(testOptions(env, true, ['--gul-electron-test']), {});
  assert.deepEqual(testOptions(env, false, []), {});
  assert.deepEqual(testOptions({ ...env, NODE_ENV: 'production' }, false, ['--gul-electron-test']), {});
});

test('preload errors expose human-readable allowlisted messages and redact unknown exceptions', async () => {
  const { publicError } = await import('../src/preload/errors.ts');
  assert.equal(
    publicError(new Error("Error invoking remote method 'gul:connect': Error: GUL_CONNECT_FAILED")).message,
    'Не удалось подключиться через VLESS REALITY. Проверьте адрес и пароль.',
  );
  for (const error of [
    new Error('https://example.test/?token=secret'),
    new Error('GUL_UNRECOGNIZED secret'),
    { message: 'secret' },
    undefined,
  ]) {
    assert.equal(publicError(error).message, 'Не удалось выполнить действие. Повторите попытку.');
  }
});
