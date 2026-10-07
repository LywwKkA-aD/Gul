import assert from 'node:assert/strict';
import test from 'node:test';
import { compileArguments, linuxCompileArguments, vcvarsCommand } from '../scripts/build-ptt.mjs';

test('native PTT build links static CRT and hardens a Windows10 x64 binary', () => {
  const args = compileArguments(
    'C:\\Gul Project\\native\\windows.cpp',
    'C:\\Gul Project\\gul-ptt.exe',
    'C:\\temp\\windows.obj',
  );
  for (const flag of [
    '/MT',
    '/O2',
    '/W4',
    '/WX',
    '/guard:cf',
    '/GS',
    '/D_WIN32_WINNT=0x0A00',
    '/DYNAMICBASE',
    '/NXCOMPAT',
    '/HIGHENTROPYVA',
  ])
    assert.ok(args.includes(flag));
  assert.ok(args.includes('C:\\Gul Project\\native\\windows.cpp'));
  assert.ok(args.includes('/FeC:\\Gul Project\\gul-ptt.exe'));
  assert.ok(args.includes('user32.lib'));
});

test('Linux portal build uses GIO argv and compiler hardening without a shell', () => {
  const args = linuxCompileArguments('/Gul Project/native/linux.cpp', '/Gul Project/gul-ptt', [
    '-I/usr/include/glib-2.0',
    '-lgio-2.0',
    '-lglib-2.0',
  ]);
  for (const flag of [
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
  ])
    assert.ok(args.includes(flag));
  assert.ok(args.includes('/Gul Project/native/linux.cpp'));
  assert.ok(args.includes('/Gul Project/gul-ptt'));
  assert.ok(args.includes('-lgio-2.0'));
});

test('vcvars batch command accepts only quoted absolute installation paths, not shell syntax', () => {
  const path =
    'C:\\Program Files (x86)\\Microsoft Visual Studio\\2022\\BuildTools\\VC\\Auxiliary\\Build\\vcvars64.bat';
  assert.equal(vcvarsCommand(path), `"call "${path}" >nul && set"`);
  for (const value of [
    'relative.bat',
    'C:\\private&execute.bat',
    'C:\\%PRIVATE%\\vcvars64.bat',
    'C:\\private".bat',
    'C:\\private\n.bat',
    'C:\\private|execute.bat',
    'C:\\private!value.bat',
  ])
    assert.throws(() => vcvarsCommand(value), /^Error: Invalid MSVC environment script$/u);
});
