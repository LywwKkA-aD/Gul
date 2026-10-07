import assert from 'node:assert/strict';
import test from 'node:test';
import { audioCompileArguments } from '../scripts/build-audio.mjs';

test('native audio helper is built as a hardened executable with explicit libpulse flags', () => {
  const arguments_ = audioCompileArguments('/source/linux.cpp', '/output/gul-audio', ['-I/pulse', '-lpulse']);
  for (const argument of [
    '-std=c++17',
    '-Wall',
    '-Wextra',
    '-Werror',
    '-fstack-protector-strong',
    '-D_FORTIFY_SOURCE=2',
    '-fPIE',
    '-pie',
    '-Wl,-z,relro,-z,now',
    '-lpulse',
  ])
    assert.ok(arguments_.includes(argument));
  assert.ok(arguments_.indexOf('/source/linux.cpp') < arguments_.indexOf('-lpulse'));
  assert.equal(arguments_[arguments_.indexOf('-o') + 1], '/output/gul-audio');
});
