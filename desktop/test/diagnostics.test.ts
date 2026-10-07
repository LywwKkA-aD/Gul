import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { DiagnosticsJournal, exportDiagnostics } from '../src/main/diagnostics.ts';

test('diagnostic records accept only known lifecycle names and allowlisted scalar metadata', () => {
  const journal = new DiagnosticsJournal(() => 1234);
  journal.record('connect-ok', {
    channelId: 1,
    state: 'connected',
    muted: true,
    pingMs: 40,
    streams: 2,
    code: 'GUL_AUDIO_FAILED',
    password: 'private',
    address: 'private',
    username: 'private',
    token: 'private',
    arbitrary: { value: 'private' },
  });
  journal.record('private-event-name', { code: 'GUL_PRIVATE_SECRET' });
  assert.deepEqual(journal.snapshot(), [
    {
      time: 1234,
      event: 'connect-ok',
      metadata: {
        channelId: 1,
        pingMs: 40,
        streams: 2,
        muted: true,
        state: 'connected',
        code: 'GUL_AUDIO_FAILED',
      },
    },
  ]);
  assert.ok(Object.isFrozen(journal.snapshot()));
  assert.ok(Object.isFrozen(journal.snapshot()[0]));
  assert.ok(Object.isFrozen(journal.snapshot()[0].metadata));
});

test('diagnostics are bounded, do not retain mutable input or secret-bearing exception messages', () => {
  const journal = new DiagnosticsJournal(() => 1000);
  const metadata = { pingMs: 22, code: 'Password contains private contents' };
  journal.record('connect-failed', metadata);
  metadata.pingMs = 99;
  assert.equal(journal.snapshot()[0].metadata.pingMs, 22);
  assert.equal(journal.snapshot()[0].metadata.code, undefined);
  for (let i = 0; i < 1000; i++)
    journal.record('media-reconnect', {
      streams: i,
      channelId: -1,
      pingMs: NaN,
      muted: 'private',
      state: 'private',
    });
  assert.equal(journal.snapshot().length, 200);
  assert.equal(journal.snapshot().at(-1)?.metadata.streams, undefined);
  journal.clear();
  assert.deepEqual(journal.snapshot(), []);
});

test('ZIP export contains environment and sanitized events, without reading arbitrary files', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-diagnostics-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const destination = join(directory, 'report.zip');
  const journal = new DiagnosticsJournal(() => 1);
  journal.record('connect-failed', { code: 'GUL_CONNECT_FAILED', password: 'do-not-export' });
  await exportDiagnostics({
    destination,
    version: '0.8.0-alpha.0',
    journal,
    electronVersion: '44.6.0',
    chromiumVersion: '152.0.0',
  });
  const zip = await readFile(destination);
  assert.equal(zip.readUInt32LE(0), 0x04034b50);
  assert.equal(zip.readUInt32LE(zip.length - 22), 0x06054b50);
  const entries: Record<string, string> = {};
  let position = 0;
  while (zip.readUInt32LE(position) === 0x04034b50) {
    const length = zip.readUInt32LE(position + 18);
    const nameLength = zip.readUInt16LE(position + 26);
    const start = position + 30 + nameLength;
    entries[zip.subarray(position + 30, start).toString()] = zip.subarray(start, start + length).toString();
    position = start + length;
  }
  assert.deepEqual(Object.keys(entries), ['info.json', 'events.json']);
  assert.equal(JSON.parse(entries['info.json']).version, '0.8.0-alpha.0');
  assert.equal(JSON.parse(entries['events.json'])[0].metadata.code, 'GUL_CONNECT_FAILED');
  assert.equal(zip.includes(Buffer.from('do-not-export')), false);
  assert.equal(zip.includes(Buffer.from(directory)), false);
  assert.ok(zip.length < 128 * 1024);
});

test('invalid version/environment strings are omitted and export failures use a safe fixed error', async (t) => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-diagnostics-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const destination = join(directory, 'report.zip');
  await exportDiagnostics({
    destination,
    version: 'private\nvalue',
    journal: new DiagnosticsJournal(),
    electronVersion: 'private-value',
  });
  const zip = await readFile(destination);
  assert.equal(zip.includes(Buffer.from('private')), false);
  await assert.rejects(
    exportDiagnostics({ destination: directory, version: '0.8.0', journal: new DiagnosticsJournal() }),
    /^Error: GUL_DIAGNOSTICS_FAILED$/u,
  );
});
