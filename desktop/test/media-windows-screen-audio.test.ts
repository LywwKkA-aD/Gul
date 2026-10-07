import assert from 'node:assert/strict';
import test from 'node:test';
import { EventEmitter } from 'node:events';
import { attachWindowsScreenAudio } from '../src/renderer/media/windows-screen-audio.ts';
import type { ScreenCapture } from '../src/renderer/media/model.ts';

class Video extends EventEmitter {
  readonly kind = 'video';
  readonly mediaStreamTrack = { readyState: 'live' };
  stops = 0;
  stop() {
    ++this.stops;
    this.mediaStreamTrack.readyState = 'ended';
  }
}
function fixture() {
  const video = new Video();
  const lease = { leaseId: 'a'.repeat(32), url: `ws://127.0.0.1:8123/${'b'.repeat(48)}` };
  const released: string[] = [];
  let notified: (id: string) => void = () => {};
  let failed: () => void = () => {};
  let unsubscribed = 0;
  let opened = 0;
  let closed = 0;
  const audio = { kind: 'audio' };
  const source = {
    track: audio as never,
    close: async () => {
      ++closed;
    },
  };
  const display = { tracks: [video] } as unknown as ScreenCapture;
  const dependencies = {
    start: async () => lease,
    stop: async (id: string) => {
      released.push(id);
    },
    onEnded: (listener: (id: string) => void) => {
      notified = listener;
      return () => {
        ++unsubscribed;
        notified = () => {};
      };
    },
    open: async (url: string, ended: () => void) => {
      assert.equal(url, lease.url);
      ++opened;
      failed = ended;
      return source;
    },
  };
  return {
    video,
    lease,
    released,
    source,
    display,
    dependencies,
    notify: (id = lease.leaseId) => notified(id),
    fail: () => failed(),
    counts: () => ({ opened, closed, unsubscribed }),
  };
}
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));

test('Windows native audio shares exactly one stereo source and releases all owned resources once', async () => {
  const f = fixture();
  const capture = await attachWindowsScreenAudio(f.display, f.dependencies);
  assert.equal(capture.tracks.length, 2);
  assert.equal(capture.tracks[1], f.source.track);
  capture.cleanup?.();
  capture.cleanup?.();
  await flush();
  assert.deepEqual(f.released, [f.lease.leaseId]);
  assert.deepEqual(f.counts(), { opened: 1, closed: 1, unsubscribed: 1 });
  assert.equal(f.video.stops, 1);
});
test('Windows refuses a Linux lease, external stream or malformed lease without opening any audio', async () => {
  for (const lease of [
    { ...fixture().lease, deviceLabel: 'default' },
    { ...fixture().lease, url: 'ws://remote.example/audio' },
    { ...fixture().lease, leaseId: 'invalid' },
  ]) {
    const f = fixture();
    await assert.rejects(
      attachWindowsScreenAudio(f.display, { ...f.dependencies, start: async () => lease as never }),
    );
    assert.equal(f.counts().opened, 0);
    assert.equal(f.video.stops, 1);
    assert.deepEqual(f.released, lease.leaseId === 'invalid' ? [] : [lease.leaseId]);
  }
});
test('ending a display during lease acquisition releases its late consent and never opens the source', async () => {
  const f = fixture();
  let resolve!: (lease: typeof f.lease) => void;
  const pending = new Promise<typeof f.lease>((yes) => {
    resolve = yes;
  });
  const capture = attachWindowsScreenAudio(f.display, { ...f.dependencies, start: () => pending });
  f.video.emit('ended');
  resolve(f.lease);
  await assert.rejects(capture);
  assert.deepEqual(f.released, [f.lease.leaseId]);
  assert.deepEqual(f.counts(), { opened: 0, closed: 0, unsubscribed: 1 });
});
test('ending a display during PCM connection closes a late source without publishing it', async () => {
  const f = fixture();
  let resolve!: (source: typeof f.source) => void;
  const pending = new Promise<typeof f.source>((yes) => {
    resolve = yes;
  });
  const capture = attachWindowsScreenAudio(f.display, { ...f.dependencies, open: () => pending });
  await flush();
  f.video.emit('ended');
  resolve(f.source);
  await assert.rejects(capture);
  assert.equal(f.counts().closed, 1);
  assert.deepEqual(f.released, [f.lease.leaseId]);
});
test('helper or PCM failure notifies the display publication and ignores a different lease', async () => {
  for (const phase of ['helper', 'pcm']) {
    const f = fixture();
    let ended = 0;
    f.video.on('ended', () => {
      ++ended;
    });
    const capture = await attachWindowsScreenAudio(f.display, f.dependencies);
    f.notify('c'.repeat(32));
    assert.equal(ended, 0);
    if (phase === 'helper') f.notify();
    else f.fail();
    capture.cleanup?.();
    await flush();
    assert.equal(ended, 1);
    assert.deepEqual(f.released, [f.lease.leaseId]);
    assert.equal(f.counts().closed, 1);
  }
});
test('an early helper exit or rejected connection cannot leave a running display or lease', async () => {
  for (const phase of ['early', 'open', 'start']) {
    const f = fixture();
    await assert.rejects(
      attachWindowsScreenAudio(f.display, {
        ...f.dependencies,
        start: async () => {
          if (phase === 'start') throw new Error('denied');
          if (phase === 'early') f.notify();
          return f.lease;
        },
        open: async () => {
          throw new Error('connection failed');
        },
      }),
    );
    assert.equal(f.video.mediaStreamTrack.readyState, 'ended');
    assert.equal(f.counts().unsubscribed, 1);
    assert.deepEqual(f.released, phase === 'start' ? [] : [f.lease.leaseId]);
  }
});
