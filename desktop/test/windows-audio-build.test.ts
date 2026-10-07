import assert from 'node:assert/strict';
import test from 'node:test';
import {
  windowsAudioCompileArguments,
  windowsAudioEnvironmentCommand,
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
