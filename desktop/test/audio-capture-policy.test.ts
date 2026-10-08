import assert from 'node:assert/strict';
import test from 'node:test';
import { installAudioCapturePolicy } from '../src/main/audio-capture-policy.ts';

function commandLine(disabled = '', enabled = '') {
  const initial = new Map([
    ['disable-features', disabled],
    ['enable-features', enabled],
  ]);
  let writes: readonly { readonly name: string; readonly value: string }[] = [];
  return {
    get writes() {
      return writes;
    },
    getSwitchValue(name: string) {
      return writes.reduce(
        (previous, entry) => (entry.name === name ? entry.value : previous),
        initial.get(name) ?? '',
      );
    },
    appendSwitch(name: string, value = '') {
      writes = [...writes, { name, value }];
    },
  };
}

for (const platform of ['darwin', 'linux', 'win32'] as const)
  test(`${platform} disables system input-volume adjustment without removing unrelated Chromium features`, () => {
    const switches = commandLine(
      'AudioServiceSandbox,ExistingCapturePolicy',
      'GlobalShortcutsPortal,OtherFeature',
    );
    installAudioCapturePolicy(switches, platform);
    assert.equal(
      switches.getSwitchValue('disable-features'),
      'AudioServiceSandbox,ExistingCapturePolicy,WebRtcAllowInputVolumeAdjustment',
    );
    assert.equal(switches.getSwitchValue('enable-features'), 'GlobalShortcutsPortal,OtherFeature');
    assert.deepEqual(
      switches.writes.map((entry) => entry.name),
      ['disable-features'],
    );
  });

test('empty disabled-feature list uses the exact Chromium feature name', () => {
  const switches = commandLine();
  installAudioCapturePolicy(switches, 'linux');
  assert.equal(switches.getSwitchValue('disable-features'), 'WebRtcAllowInputVolumeAdjustment');
});

test('policy is idempotent when input-volume adjustment was already disabled', () => {
  const switches = commandLine('AudioServiceSandbox, WebRtcAllowInputVolumeAdjustment ');
  installAudioCapturePolicy(switches, 'linux');
  installAudioCapturePolicy(switches, 'linux');
  assert.equal(
    switches.getSwitchValue('disable-features'),
    'AudioServiceSandbox, WebRtcAllowInputVolumeAdjustment ',
  );
  assert.deepEqual(switches.writes, []);
});

test('unsupported platform leaves Chromium feature switches untouched', () => {
  const switches = commandLine('ExistingCapturePolicy', 'OtherFeature');
  installAudioCapturePolicy(switches, 'freebsd');
  assert.deepEqual(switches.writes, []);
  assert.equal(switches.getSwitchValue('disable-features'), 'ExistingCapturePolicy');
  assert.equal(switches.getSwitchValue('enable-features'), 'OtherFeature');
});
