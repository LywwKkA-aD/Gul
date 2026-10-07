import assert from 'node:assert/strict';
import test from 'node:test';
import { RoomEvent } from 'livekit-client';
import { createRoom, disconnect } from '../src/renderer/media/rooms.ts';

test('one explicit 48k context owns voice/screen playback and closes on unexpected SDK disconnect exactly once', async () => {
  let closes = 0;
  let captured: any;
  const context = {
    sampleRate: 48000,
    close: async () => {
      closes++;
    },
  };
  const handlers = new Map<string, (() => void)[]>();
  const room = createRoom({
    context: () => context as any,
    room: (options) => {
      captured = options;
      return {
        on(event: string, callback: () => void) {
          handlers.set(event, [...(handlers.get(event) ?? []), callback]);
          return this;
        },
        async disconnect() {
          handlers.get(RoomEvent.Disconnected)?.forEach((callback) => callback());
        },
      } as any;
    },
  });
  assert.equal(captured.webAudioMix.audioContext, context);
  assert.equal(captured.audioCaptureDefaults.sampleRate, 48000);
  handlers.get(RoomEvent.Disconnected)?.forEach((callback) => callback());
  await disconnect(room);
  await disconnect(room);
  assert.equal(closes, 1);
});
test('failed room construction releases the new context and failed SDK disconnect still releases it', async () => {
  let closes = 0;
  const context = () =>
    ({
      close: async () => {
        closes++;
      },
    }) as any;
  assert.throws(() =>
    createRoom({
      context,
      room: () => {
        throw new Error('Room unavailable.');
      },
    }),
  );
  const room = createRoom({
    context,
    room: () =>
      ({
        on() {
          return this;
        },
        disconnect: async () => {
          throw new Error();
        },
      }) as any,
  });
  await disconnect(room);
  await disconnect();
  assert.equal(closes, 2);
});
test('failed SDK event setup and a later disconnect share the same context cleanup', async () => {
  let closes = 0;
  let disconnected!: () => void;
  assert.throws(
    () =>
      createRoom({
        context: () =>
          ({
            close: async () => {
              closes++;
            },
          }) as any,
        room: () =>
          ({
            on(_event: string, callback: () => void) {
              disconnected = callback;
              throw new Error('Event setup unavailable.');
            },
          }) as any,
      }),
    /звук канала/u,
  );
  disconnected();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(closes, 1);
});
