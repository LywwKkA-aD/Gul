import assert from 'node:assert/strict';
import test from 'node:test';
import {
  captureCapabilities,
  CaptureChooser,
  capturePickerMode,
  captureSourceLabel,
} from '../src/main/capture-policy.ts';

const selected = { id: 'window:42:0', name: '' };
const options = {
  valid: () => true,
  getSources: async () => [selected],
  pick: async () => {
    throw new Error('The portal already obtained consent');
  },
  capabilities: captureCapabilities('linux', '7.0.0', true),
  audioRequested: false,
  portalSelection: true,
};

test('a successful single portal selection is used once without a second application picker', async () => {
  assert.deepEqual(await new CaptureChooser().choose(options), { video: selected });
});

test('portal cancellation, unexpected source counts and stale consent cannot grant capture', async () => {
  const chooser = new CaptureChooser();
  assert.equal(await chooser.choose({ ...options, getSources: async () => [] }), null);
  assert.equal(await chooser.choose({ ...options, getSources: async () => [selected, selected] }), null);
  let valid = true;
  assert.equal(
    await chooser.choose({
      ...options,
      valid: () => valid,
      getSources: async () => {
        valid = false;
        return [selected];
      },
    }),
    null,
  );
  await assert.rejects(
    chooser.choose({
      ...options,
      getSources: async () => {
        throw new Error('Portal cancelled');
      },
    }),
  );
  assert.deepEqual(await chooser.choose(options), { video: selected });
});

test('portal consent is enabled only for the exact Linux Wayland backend environment', () => {
  assert.equal(
    capturePickerMode('linux', { XDG_SESSION_TYPE: 'wayland', WAYLAND_DISPLAY: 'wayland-0' }),
    'portal',
  );
  assert.equal(
    capturePickerMode('linux', { XDG_SESSION_TYPE: 'x11', WAYLAND_DISPLAY: 'wayland-0' }),
    'application',
  );
  assert.equal(capturePickerMode('linux', { XDG_SESSION_TYPE: 'wayland' }), 'application');
  assert.equal(
    capturePickerMode('win32', { XDG_SESSION_TYPE: 'wayland', WAYLAND_DISPLAY: 'wayland-0' }),
    'application',
  );
});

test('application picker labels stay meaningful even when native source titles are empty', () => {
  assert.equal(captureSourceLabel({ id: 'screen:12:0', name: '  ' }, 1), 'Экран 2');
  assert.equal(captureSourceLabel(selected, 0), 'Окно 1');
  assert.equal(captureSourceLabel({ id: 'unknown', name: '' }, 0), 'Источник 1');
  assert.equal(captureSourceLabel({ id: 'window:12:0', name: 'Game' }, 0), 'Game');
});
