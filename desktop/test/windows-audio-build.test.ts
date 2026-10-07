import assert from 'node:assert/strict';
import test from 'node:test';
import {
  windowsAudioCompileArguments,
  windowsAudioEnvironmentCommand,
  buildWindowsAudio,
  probeWindowsAudio,
} from '../scripts/build-windows-audio.mjs';

test('Windows screen audio ships a hardened self-contained executable without driver installation', () => {
  const args = windowsAudioCompileArguments('source.cpp', 'gul-audio.exe', 'audio.obj');
  for (const expected of [
    '/MT',
    '/W4',
    '/WX',
    '/guard:cf',
    '/GS',
    '/DYNAMICBASE',
    '/NXCOMPAT',
    '/HIGHENTROPYVA',
  ])
    assert.ok(args.includes(expected));
  for (const expected of ['ole32.lib', 'advapi32.lib', 'mmdevapi.lib']) assert.ok(args.includes(expected));
  assert.ok(args.includes('/Fegul-audio.exe'));
  assert.ok(args.includes('/Foaudio.obj'));
  assert.ok(!args.some((arg: string) => /driver|inf|setup/iu.test(arg)));
});
test('Windows audio compiler environment accepts only an absolute trusted bat script', () => {
  assert.equal(
    windowsAudioEnvironmentCommand('C:\\Tools\\VC\\vcvars64.bat'),
    '"call "C:\\Tools\\VC\\vcvars64.bat" >nul && set"',
  );
  for (const path of [
    'relative.bat',
    'C:\\evil.bat&run',
    'C:\\%EVIL%\\x.bat',
    'C:\\evil!x!.bat',
    'C:\\evil\n.bat',
    'C:\\x.exe',
  ])
    assert.throws(() => windowsAudioEnvironmentCommand(path), /Invalid MSVC/);
});
test('Windows native audio build is a no-op on other platforms', async () => {
  if (process.platform !== 'win32') assert.equal(await buildWindowsAudio(), undefined);
});
test('native Windows capability probe observes real initialization without starting PCM capture', () => {
  let calls = 0;
  assert.equal(
    probeWindowsAudio('helper.exe', (file, args, options) => {
      ++calls;
      assert.equal(file, 'helper.exe');
      assert.deepEqual(args, ['--probe']);
      assert.deepEqual(options.stdio, ['ignore', 'pipe', 'pipe']);
      assert.equal(options.timeout, 10000);
      assert.equal(options.maxBuffer, 64);
      return 'SUPPORTED\n';
    }),
    true,
  );
  assert.equal(calls, 1);
  for (const value of ['', 'SUPPORTED', 'UNSUPPORTED\n', 'SUPPORTED\nextra'])
    assert.equal(
      probeWindowsAudio('helper.exe', () => value),
      false,
    );
  assert.equal(
    probeWindowsAudio('helper.exe', () => {
      throw new Error('private runtime details');
    }),
    false,
  );
});
