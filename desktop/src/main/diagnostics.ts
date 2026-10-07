import { writePrivateBytes } from './storage.ts';

const EVENTS = new Set([
  'app-start',
  'connect-start',
  'connect-ok',
  'connect-failed',
  'channel-change',
  'media-reconnect',
  'media-disconnected',
  'capture-start',
  'capture-stop',
  'capture-failed',
  'storage-unavailable',
  'update-check-failed',
  'shortcut-failed',
]);
const CODES = new Set([
  'GUL_INPUT_INVALID',
  'GUL_CONNECT_FAILED',
  'GUL_GRANT_INVALID',
  'GUL_STATE_INVALID',
  'GUL_SESSION_STALE',
  'GUL_NOT_CONNECTED',
  'GUL_STATE_FAILED',
  'GUL_CHANNEL_FAILED',
  'GUL_AUDIO_FAILED',
  'GUL_SCREEN_FAILED',
  'GUL_IPC_DENIED',
  'GUL_SHORTCUT_UNAVAILABLE',
  'GUL_DIAGNOSTICS_FAILED',
  'GUL_STORAGE_FAILED',
]);
type Scalar = string | number | boolean;
export interface DiagnosticRecord {
  readonly time: number;
  readonly event: string;
  readonly metadata: Readonly<Record<string, Scalar>>;
}
const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
function sanitizedMetadata(input: unknown): Readonly<Record<string, Scalar>> {
  if (!record(input)) return Object.freeze({});
  const numeric = (key: string, maximum: number) =>
    typeof input[key] === 'number' &&
    Number.isFinite(input[key]) &&
    (input[key] as number) >= 0 &&
    (input[key] as number) <= maximum
      ? { [key]: input[key] as number }
      : {};
  return Object.freeze({
    ...numeric('channelId', 3),
    ...numeric('pingMs', 60000),
    ...numeric('streams', 128),
    ...(typeof input.muted === 'boolean' ? { muted: input.muted } : {}),
    ...(typeof input.deafened === 'boolean' ? { deafened: input.deafened } : {}),
    ...(typeof input.state === 'string' &&
    ['connected', 'connecting', 'reconnecting', 'disconnected'].includes(input.state)
      ? { state: input.state }
      : {}),
    ...(typeof input.code === 'string' && CODES.has(input.code) ? { code: input.code } : {}),
  });
}

/** Record structure, not raw exceptions, IPC arguments, paths or network addresses. */
export class DiagnosticsJournal {
  private records: readonly DiagnosticRecord[] = Object.freeze([]);
  private readonly now: () => number;
  constructor(now: () => number = Date.now) {
    this.now = now;
  }
  record(event: string, metadata: unknown = {}): void {
    if (!EVENTS.has(event)) return;
    const now = this.now();
    const time = Number.isSafeInteger(now) && now >= 0 ? now : 0;
    this.records = Object.freeze(
      [...this.records, Object.freeze({ time, event, metadata: sanitizedMetadata(metadata) })].slice(-200),
    );
  }
  snapshot(): readonly DiagnosticRecord[] {
    return this.records;
  }
  clear(): void {
    this.records = Object.freeze([]);
  }
}

const CRC_TABLE = Object.freeze(
  Array.from({ length: 256 }, (_, index) => {
    let crc = index;
    for (let bit = 0; bit < 8; bit++) crc = crc & 1 ? 0xedb88320 ^ (crc >>> 1) : crc >>> 1;
    return crc >>> 0;
  }),
);
function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff;
  for (const byte of bytes) crc = CRC_TABLE[(crc ^ byte) & 255] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}
function archive(files: readonly { readonly name: string; readonly value: unknown }[]): Buffer {
  const locals: Buffer[] = [],
    directories: Buffer[] = [];
  let offset = 0;
  for (const file of files) {
    const name = Buffer.from(file.name),
      data = Buffer.from(JSON.stringify(file.value, null, 2));
    const crc = crc32(data),
      local = Buffer.alloc(30),
      directory = Buffer.alloc(46);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0x800, 6);
    local.writeUInt16LE(33, 12);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(data.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(name.length, 26);
    directory.writeUInt32LE(0x02014b50, 0);
    directory.writeUInt16LE(20, 4);
    directory.writeUInt16LE(20, 6);
    directory.writeUInt16LE(0x800, 8);
    directory.writeUInt16LE(33, 14);
    directory.writeUInt32LE(crc, 16);
    directory.writeUInt32LE(data.length, 20);
    directory.writeUInt32LE(data.length, 24);
    directory.writeUInt16LE(name.length, 28);
    directory.writeUInt32LE(offset, 42);
    locals.push(local, name, data);
    directories.push(directory, name);
    offset += local.length + name.length + data.length;
  }
  const directory = Buffer.concat(directories),
    end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}
const versionValue = (value: string | undefined) =>
  typeof value === 'string' && value.length <= 128 && /^v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/u.test(value)
    ? value
    : 'unknown';
export async function exportDiagnostics(options: {
  readonly destination: string;
  readonly version: string;
  readonly journal: DiagnosticsJournal;
  readonly electronVersion?: string;
  readonly chromiumVersion?: string;
}): Promise<void> {
  const data = archive([
    {
      name: 'info.json',
      value: {
        application: 'Gul',
        version: versionValue(options.version),
        electron: versionValue(options.electronVersion),
        chromium: versionValue(options.chromiumVersion),
        platform: process.platform,
        architecture: process.arch,
        node: process.versions.node,
      },
    },
    { name: 'events.json', value: options.journal.snapshot() },
  ]);
  if (!(await writePrivateBytes(options.destination, data))) throw new Error('GUL_DIAGNOSTICS_FAILED');
}
