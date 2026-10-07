import assert from 'node:assert/strict';
import test from 'node:test';
import { CapturePicker } from '../src/main/capture-picker.ts';
import type { CapturePickerRequest } from '../src/shared/capture-picker.ts';

const png = 'data:image/png;base64,iVBORw0KGgo=';
const sources = Object.freeze([
  Object.freeze({ id: 'screen:0:0', name: 'Экран 1', thumbnail: { toDataURL: () => png } }),
  Object.freeze({ id: 'window:123:0', name: 'Игра', thumbnail: { toDataURL: () => png } }),
]);
function fixture() {
  const events: (CapturePickerRequest | null)[] = [];
  let sequence = 0;
  const picker = new CapturePicker({
    push: (event) => events.push(event),
    nonce: () => (++sequence).toString(16).padStart(32, '0'),
  });
  return { picker, events };
}

test('source IDs remain in main while opaque card selection resolves the original index', async () => {
  const { picker, events } = fixture();
  const choice = picker.choose(sources, true, 'Звук компьютера', () => true);
  const prompt = events[0]!;
  assert.equal(prompt.audio, true);
  assert.deepEqual(
    prompt.sources.map((source) => source.kind),
    ['screen', 'window'],
  );
  assert.equal(prompt.sources[1].thumbnail, png);
  assert.doesNotMatch(JSON.stringify(prompt), /screen:0:0|window:123:0/u);
  assert.notEqual(prompt.sources[0].sourceKey, prompt.sources[1].sourceKey);
  assert.equal(picker.select(prompt.requestId, prompt.sources[1].sourceKey), true);
  assert.deepEqual(await choice, { response: 2, checkboxChecked: true });
  assert.equal(events.at(-1), null);
});

test('explicit cancellation never grants video or audio and duplicate replies are rejected', async () => {
  const { picker, events } = fixture();
  const choice = picker.choose(sources, true, '', () => true);
  const prompt = events[0]!;
  assert.equal(picker.select(prompt.requestId, null), true);
  assert.deepEqual(await choice, { response: 0, checkboxChecked: false });
  assert.equal(picker.select(prompt.requestId, prompt.sources[0].sourceKey), false);
});

test('foreign or malformed replies cannot select a card or cancel the current request', async () => {
  const { picker, events } = fixture();
  const choice = picker.choose(sources, false, '', () => true);
  const prompt = events[0]!;
  assert.equal(picker.select('f'.repeat(32), prompt.sources[0].sourceKey), false);
  assert.equal(picker.select(prompt.requestId, 'screen:0:0'), false);
  assert.equal(picker.select(prompt.requestId, undefined), false);
  assert.equal(picker.select({}, null), false);
  assert.equal(events.length, 1);
  assert.equal(picker.select(prompt.requestId, prompt.sources[0].sourceKey), true);
  assert.deepEqual(await choice, { response: 1, checkboxChecked: false });
});

test('an expired epoch cannot turn a late click into consent or select the next request', async () => {
  const { picker, events } = fixture();
  let valid = true;
  const first = picker.choose(sources, true, '', () => valid);
  const old = events[0]!;
  valid = false;
  assert.equal(picker.select(old.requestId, old.sources[0].sourceKey), false);
  assert.deepEqual(await first, { response: 0, checkboxChecked: false });
  const second = picker.choose(sources, false, '', () => true);
  const current = events.at(-1)!;
  assert.notEqual(current.requestId, old.requestId);
  assert.equal(picker.select(old.requestId, old.sources[0].sourceKey), false);
  assert.equal(picker.select(current.requestId, current.sources[1].sourceKey), true);
  assert.deepEqual(await second, { response: 2, checkboxChecked: false });
});

test('a second valid picker cannot replace the pending explicit user choice', async () => {
  const { picker, events } = fixture();
  const first = picker.choose(sources, true, '', () => true);
  assert.deepEqual(await picker.choose(sources, true, '', () => true), {
    response: 0,
    checkboxChecked: false,
  });
  assert.equal(events.length, 1);
  picker.cancel();
  assert.deepEqual(await first, { response: 0, checkboxChecked: false });
});

test('invalid validity checks, empty sources and failed delivery fail closed', async () => {
  const { picker, events } = fixture();
  assert.deepEqual(await picker.choose(sources, true, '', () => false), {
    response: 0,
    checkboxChecked: false,
  });
  assert.deepEqual(await picker.choose([], true, '', () => true), { response: 0, checkboxChecked: false });
  assert.deepEqual(
    await picker.choose(sources, true, '', () => {
      throw Error('fixture');
    }),
    { response: 0, checkboxChecked: false },
  );
  assert.equal(events.length, 0);
  const failed = new CapturePicker({
    push: () => {
      throw Error('closed window');
    },
  });
  assert.deepEqual(await failed.choose(sources, true, '', () => true), {
    response: 0,
    checkboxChecked: false,
  });
});

test('previews and text are bounded; non-PNG data and thumbnail errors are not exposed', async () => {
  const { picker, events } = fixture();
  const entries = Array.from({ length: 130 }, (_, i) => ({
    id: `window:${i}:0`,
    name: '<script>' + 'x'.repeat(300) + '\u0000',
    thumbnail: {
      toDataURL: () =>
        i === 0
          ? 'data:text/html;base64,Zm9v'
          : i === 1
            ? 'data:image/png;base64,' + 'A'.repeat(1_100_000)
            : i === 2
              ? (() => {
                  throw Error('thumbnail failed');
                })()
              : png,
    },
  }));
  const choice = picker.choose(entries, false, 'd'.repeat(3000), () => true);
  const prompt = events[0]!;
  assert.equal(prompt.sources.length, 120);
  assert.ok(prompt.sources.every((source) => source.name.length <= 240 && !source.name.includes('\u0000')));
  assert.equal(prompt.details.length, 2000);
  assert.deepEqual(
    prompt.sources.slice(0, 3).map((source) => source.thumbnail),
    [null, null, null],
  );
  assert.ok(Object.isFrozen(prompt) && Object.isFrozen(prompt.sources) && Object.isFrozen(prompt.sources[0]));
  picker.cancel();
  await choice;
});

test('only screen/window entries are exposed and the response still indexes the unfiltered source list', async () => {
  const { picker, events } = fixture();
  const choice = picker.choose([{ id: 'invalid', name: 'unknown' }, ...sources], false, '', () => true);
  const prompt = events[0]!;
  assert.equal(prompt.sources.length, 2);
  picker.select(prompt.requestId, prompt.sources[0].sourceKey);
  assert.deepEqual(await choice, { response: 2, checkboxChecked: false });
});

test('nonce collisions fail closed instead of giving two cards the same authority', async () => {
  const picker = new CapturePicker({ push: () => {}, nonce: () => 'a'.repeat(32) });
  assert.deepEqual(await picker.choose(sources, true, '', () => true), {
    response: 0,
    checkboxChecked: false,
  });
});

test('epoch invalidation closes the modal without requiring a user click', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval', 'setTimeout'] });
  const { picker, events } = fixture();
  let valid = true;
  const choice = picker.choose(sources, true, '', () => valid);
  valid = false;
  t.mock.timers.tick(100);
  assert.deepEqual(await choice, { response: 0, checkboxChecked: false });
  assert.equal(events.at(-1), null);
  const count = events.length;
  t.mock.timers.tick(120_000);
  assert.equal(events.length, count, 'cancelled request retains no polling or deadline');
});

test('an abandoned renderer request times out and releases the next picker', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval', 'setTimeout'] });
  const { picker, events } = fixture();
  const choice = picker.choose(sources, true, '', () => true);
  t.mock.timers.tick(120_000);
  assert.deepEqual(await choice, { response: 0, checkboxChecked: false });
  const next = picker.choose(sources, false, '', () => true);
  assert.ok(events.at(-1)?.requestId);
  picker.cancel();
  await next;
});

test('the total preview budget prevents excessive IPC even with many valid thumbnails', async () => {
  const { picker, events } = fixture();
  const image = 'data:image/png;base64,iVBORw0KGgo' + 'A'.repeat(900_000);
  const choice = picker.choose(
    Array.from({ length: 20 }, (_, index) => ({
      id: `screen:${index}:0`,
      name: '',
      thumbnail: { toDataURL: () => image },
    })),
    false,
    '',
    () => true,
  );
  const prompt = events[0]!;
  assert.ok(
    prompt.sources.reduce((sum, source) => sum + (source.thumbnail?.length ?? 0), 0) <= 12 * 1024 * 1024,
  );
  assert.ok(prompt.sources.some((source) => source.thumbnail === null));
  assert.equal(prompt.sources[0].name, 'Экран 1');
  picker.cancel();
  await choice;
});
