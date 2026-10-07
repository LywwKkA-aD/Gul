import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { conformVp8Placeholders, installBundleWorkaround } from '../src/renderer/media/sdp-bundle.ts';

const section = (mid: string, fmtp = '', codec = 'VP8/90000', type = 'video') =>
  `m=${type} 9 UDP/TLS/RTP/SAVPF 96\r\na=mid:${mid}\r\na=rtpmap:96 ${codec}\r\n${fmtp ? `a=fmtp:96 ${fmtp}\r\n` : ''}a=recvonly\r\n`;
const header = 'v=0\r\na=group:BUNDLE 0 1 2\r\n';

test('missing VP8 placeholder fmtp receives screen bitrate without touching real sections or other SDP fields', () => {
  const real = section('0', 'x-google-start-bitrate=1800');
  const placeholder = section('1');
  const audio = section('2', 'stereo=1', 'opus/48000/2', 'audio');
  const sdp = header + real + placeholder + audio;
  const result = conformVp8Placeholders(sdp, new Set(['1', '2']));
  assert.equal(result, header + real + section('1', 'x-google-start-bitrate=1800') + audio);
  assert.equal(conformVp8Placeholders(result, new Set(['1', '2'])), result);
});

test('reverted transceivers with retained msid remove stale bitrate when active VP8 has none', () => {
  const real = section('0');
  const placeholder = section('1', 'x-google-start-bitrate=1800') + 'a=msid:old old-track\r\n';
  assert.equal(
    conformVp8Placeholders(header + real + placeholder, new Set(['1'])),
    header + real + section('1') + 'a=msid:old old-track\r\n',
  );
});

test('only matching VP8 in the same BUNDLE group is conformed; existing non-bitrate params remain intact', () => {
  const same = section('0', 'max-fs=3600;x-google-start-bitrate=1800') + section('1', 'max-fs=3600');
  assert.equal(
    conformVp8Placeholders(header + same, new Set(['1'])),
    header +
      section('0', 'max-fs=3600;x-google-start-bitrate=1800') +
      section('1', 'max-fs=3600;x-google-start-bitrate=1800'),
  );
  for (const other of [section('1', '', 'H264/90000'), section('1', '', 'VP8/80000'), section('9')]) {
    const sdp = header + section('0', 'x-google-start-bitrate=1800') + other;
    assert.equal(conformVp8Placeholders(sdp, new Set(['1', '9'])), sdp);
  }
  const outside = 'v=0\r\n' + same;
  assert.equal(conformVp8Placeholders(outside, new Set(['1'])), outside);
  const already =
    header + section('0', 'x-google-start-bitrate=1800') + section('1', 'x-google-start-bitrate=1800');
  assert.equal(conformVp8Placeholders(already, new Set(['1'])), already);
});

test('separate BUNDLE groups do not borrow fmtp and real codec profile differences are never rewritten', () => {
  const sdp =
    'v=0\r\na=group:BUNDLE 0 1\r\na=group:BUNDLE 2 3\r\n' +
    section('0', 'x-google-start-bitrate=1800') +
    section('1') +
    section('2') +
    section('3');
  assert.equal(
    conformVp8Placeholders(sdp, new Set(['1', '3'])),
    sdp.replace(section('1'), section('1', 'x-google-start-bitrate=1800')),
  );
  const incompatible =
    header + section('0', 'max-fs=3600;x-google-start-bitrate=1800') + section('1', 'max-fs=1000');
  assert.equal(conformVp8Placeholders(incompatible, new Set(['1'])), incompatible);
  const realConflict =
    header + section('0', 'x-google-start-bitrate=1800') + section('1', 'x-google-start-bitrate=1000');
  assert.equal(conformVp8Placeholders(realConflict, new Set()), realConflict);
});

test('zero-port bundle-only video remains bundled; rejected media and LF line endings remain intact', () => {
  const real = section('0', 'x-google-start-bitrate=1800');
  const rejected = section('2').replace('m=video 9 ', 'm=video 0 ');
  const bundled = section('1').replace('m=video 9 ', 'm=video 0 ') + 'a=bundle-only\r\n';
  const sdp = header + real + bundled + rejected;
  const expected =
    header +
    real +
    section('1', 'x-google-start-bitrate=1800').replace('m=video 9 ', 'm=video 0 ') +
    'a=bundle-only\r\n' +
    rejected;
  assert.equal(conformVp8Placeholders(sdp, new Set(['1', '2'])), expected);
  assert.equal(
    conformVp8Placeholders(sdp.replaceAll('\r\n', '\n'), new Set(['1', '2'])),
    expected.replaceAll('\r\n', '\n'),
  );
});

test('owned SDK publisher repairs offers and SDK-munged answers; remote offers and original SDP stay untouched', async () => {
  const engine = new EventEmitter();
  const called: { sd: RTCSessionDescriptionInit; munged?: string; remote?: boolean }[] = [];
  const publisher = {
    getTransceivers: () => [
      { mid: '0', sender: { track: {} } },
      { mid: '1', sender: { track: null } },
    ],
    async setMungedSDP(sd: RTCSessionDescriptionInit, munged?: string, remote?: boolean) {
      called.push({ sd, munged, remote });
    },
  };
  const original = publisher.setMungedSDP;
  installBundleWorkaround({ engine } as any);
  engine.emit('transportsCreated', publisher);
  const sdp = header + section('0', 'x-google-start-bitrate=1800') + section('1');
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp);
  assert.equal(called[0].munged, conformVp8Placeholders(sdp, new Set(['1'])));
  await publisher.setMungedSDP({ type: 'answer', sdp }, sdp, true);
  assert.equal(called[1].munged, conformVp8Placeholders(sdp, new Set(['1'])));
  assert.equal(called[1].sd.sdp, sdp, 'the original SFU description remains available to the SDK fallback');
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp, true);
  assert.equal(called[2].munged, sdp, 'subscriber/server offers are never conformed');
  await publisher.setMungedSDP({ type: 'rollback' });
  assert.equal(called[3].munged, undefined);
  engine.emit('transportsCreated', publisher);
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp);
  assert.equal(called.length, 5, 'a reconnect event cannot stack wrappers');
  assert.notEqual(publisher.setMungedSDP, original);
});

test('full Room engine recreation and null transceiver mids cannot lose or broaden the workaround', async () => {
  const room = { engine: new EventEmitter() };
  installBundleWorkaround(room as any);
  installBundleWorkaround(room as any);
  room.engine = new EventEmitter();
  room.engine = room.engine;
  assert.equal(room.engine.listenerCount('transportsCreated'), 1);
  let received = '';
  const publisher = {
    getTransceivers: () => [{ mid: null as string | null, sender: { track: null } }],
    async setMungedSDP(_sd: RTCSessionDescriptionInit, munged?: string) {
      received = munged ?? '';
    },
  };
  room.engine.emit('transportsCreated', publisher);
  const sdp =
    header +
    section('0', 'x-google-start-bitrate=1800').replace('recvonly', 'sendonly') +
    section('1').replace('recvonly', 'sendrecv');
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp);
  assert.equal(received, sdp, 'unknown mids must not cause an arbitrary media section rewrite');
  publisher.getTransceivers = () => [{ mid: '1', sender: { track: null } }];
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp);
  assert.equal(received, conformVp8Placeholders(sdp, new Set(['1'])));
  assert.throws(() => room.engine.emit('transportsCreated', {}), /несовместима/);
});

test('first SDK offer identifies explicit non-sending SDP sections before transceiver mids are assigned', async () => {
  const engine = new EventEmitter();
  let received = '';
  const publisher = {
    getTransceivers: () => [
      { mid: null, sender: { track: null } },
      { mid: null, sender: { track: { readyState: 'live' } } },
    ],
    async setMungedSDP(_sd: RTCSessionDescriptionInit, munged?: string) {
      received = munged ?? '';
    },
  };
  installBundleWorkaround({ engine } as any);
  engine.emit('transportsCreated', publisher);
  const real = section('1', 'x-google-start-bitrate=1800').replace('recvonly', 'sendonly');
  const sdp = header + section('0') + real;
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp);
  assert.equal(received, header + section('0', 'x-google-start-bitrate=1800') + real);
});

test('an inactive SDK transceiver with its ended sender track is still an unused local section', async () => {
  const engine = new EventEmitter();
  let received = '';
  const publisher = {
    getTransceivers: () => [
      { mid: '0', direction: 'sendonly', sender: { track: { readyState: 'live' } } },
      { mid: '1', direction: 'inactive', sender: { track: { readyState: 'ended' } } },
    ],
    async setMungedSDP(_sd: RTCSessionDescriptionInit, munged?: string) {
      received = munged ?? '';
    },
  };
  installBundleWorkaround({ engine } as any);
  engine.emit('transportsCreated', publisher);
  const real = section('0', 'x-google-start-bitrate=1800').replace('recvonly', 'sendonly');
  const stale = section('1').replace('recvonly', 'inactive') + 'a=msid:old old-track\r\n';
  const sdp = header + real + stale;
  await publisher.setMungedSDP({ type: 'offer', sdp }, sdp);
  assert.equal(
    received,
    header +
      real +
      stale
        .replace('a=recvonly', 'a=inactive')
        .replace(
          'a=rtpmap:96 VP8/90000\r\n',
          'a=rtpmap:96 VP8/90000\r\na=fmtp:96 x-google-start-bitrate=1800\r\n',
        ),
  );
});
