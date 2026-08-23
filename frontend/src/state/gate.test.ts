import test from 'node:test';
import assert from 'node:assert/strict';
import {
  VAD_HANGOVER_CHOICES,
  VAD_HANGOVER_DEFAULT,
  snapHangoverMs,
} from './gate.ts';

test('an offered tail is left alone', () => {
  for (const ms of VAD_HANGOVER_CHOICES) {
    assert.equal(snapHangoverMs(ms), ms);
  }
});

test('a tail between two offered ones takes the nearer', () => {
  assert.equal(snapHangoverMs(140), 100);
  assert.equal(snapHangoverMs(160), 200);
  assert.equal(snapHangoverMs(600), 500);
});

test('a tail outside the offered range takes the nearest edge', () => {
  assert.equal(snapHangoverMs(0), 100);
  assert.equal(snapHangoverMs(60000), 1000);
});

test('a tail that is not a number falls back to the default', () => {
  assert.equal(snapHangoverMs(NaN), VAD_HANGOVER_DEFAULT);
});
