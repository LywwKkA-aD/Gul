import assert from 'node:assert/strict';
import test from 'node:test';
import { PreferenceUpdateQueue, mergePreferences } from '../src/renderer/preference-updates.ts';
import { defaultVoiceSettings } from '../src/renderer/media/voice-gate.ts';
import type { Preferences } from '../src/renderer/preferences.ts';

const initial: Preferences = {
  audioinput: 'default',
  audiooutput: 'default',
  shortcut: 'F8',
  toggleEnabled: false,
  voice: defaultVoiceSettings,
  soundNotifications: false,
  hotkeyMode: 'toggle',
  screenQuality: '720p30',
};
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
test('reopening settings during pending voice work cannot overwrite a newer output device', async () => {
  const queue = new PreferenceUpdateQueue();
  const pending = deferred();
  let saved = initial;
  const actual: string[] = [];
  const voice = queue.run(
    { voice: { noiseSuppression: false } },
    () => saved,
    async (next, patch) => {
      await pending.promise;
      actual.push(`ns:${next.voice.noiseSuppression}`);
      saved = mergePreferences(saved, patch);
    },
  );
  const output = queue.run(
    { audiooutput: 'headphones' },
    () => saved,
    async (next, patch) => {
      actual.push(next.audiooutput);
      saved = mergePreferences(saved, patch);
    },
  );
  pending.resolve();
  await Promise.all([voice, output]);
  assert.deepEqual(actual, ['ns:false', 'headphones']);
  assert.equal(saved.audiooutput, 'headphones');
  assert.equal(saved.voice.noiseSuppression, false);
});
test('an explicit voice patch from the reopened dialog preserves another pending setting', async () => {
  const queue = new PreferenceUpdateQueue();
  const pending = deferred();
  let saved = initial;
  const first = queue.run(
    { voice: { noiseSuppression: false } },
    () => saved,
    async (_next, patch) => {
      await pending.promise;
      saved = mergePreferences(saved, patch);
    },
  );
  const second = queue.run(
    { voice: { inputGain: 1.5 } },
    () => saved,
    async (next, patch) => {
      assert.equal(next.voice.noiseSuppression, false);
      saved = mergePreferences(saved, patch);
    },
  );
  pending.resolve();
  await Promise.all([first, second]);
  assert.equal(saved.voice.noiseSuppression, false);
  assert.equal(saved.voice.inputGain, 1.5);
});
test('a failed update leaves preferences intact and does not block the next change', async () => {
  const queue = new PreferenceUpdateQueue();
  let saved = initial;
  const first = queue.run(
    { audioinput: 'unavailable' },
    () => saved,
    async () => {
      throw new Error('device lost');
    },
  );
  const second = queue.run(
    { audiooutput: 'headphones' },
    () => saved,
    async (_next, patch) => {
      saved = mergePreferences(saved, patch);
    },
  );
  await assert.rejects(first);
  await second;
  assert.equal(saved.audioinput, 'default');
  assert.equal(saved.audiooutput, 'headphones');
});
test('successful voice work preserves unrelated preferences changed while it awaited a device', async () => {
  const queue = new PreferenceUpdateQueue();
  const pending = deferred();
  let saved = initial;
  const update = queue.run(
    { voice: { inputGain: 1.2 } },
    () => saved,
    async (_next, patch) => {
      await pending.promise;
      saved = mergePreferences(saved, patch);
    },
  );
  saved = { ...saved, soundNotifications: true };
  pending.resolve();
  await update;
  assert.equal(saved.soundNotifications, true);
  assert.equal(saved.voice.inputGain, 1.2);
  assert.equal(Object.isFrozen(saved), true);
});
test('dragging back to the saved gain preserves that explicit intent while an older change is pending', async () => {
  const queue = new PreferenceUpdateQueue();
  const pending = deferred();
  let saved = initial;
  const applied: number[] = [];
  const apply = async (_next: Preferences, patch: Parameters<typeof mergePreferences>[1]) => {
    await pending.promise;
    saved = mergePreferences(saved, patch);
    applied.push(saved.voice.inputGain);
  };
  const first = queue.run({ voice: { inputGain: 1.5 } }, () => saved, apply);
  const last = queue.run({ voice: { inputGain: 1 } }, () => saved, apply);
  pending.resolve();
  await Promise.all([first, last]);
  assert.deepEqual(applied, [1.5, 1]);
  assert.equal(saved.voice.inputGain, 1);
});
