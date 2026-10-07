const preamble = Buffer.from('GULAUD1\0', 'ascii');
const sessionBytes = 24;
const frameHeaderBytes = 16;
const maxFrames = 480;
const frameMagic = 0x314c5547;
const failure = () => new Error('GUL_AUDIO_PROTOCOL');

/** Fixed 48 kHz, two channel float PCM. Never accepts arbitrary formats or unbounded lengths. */
export class WindowsAudioFrames {
  private buffer = Buffer.alloc(0);
  private ready = false;
  private closed = false;
  private sequence: number | undefined;
  private readonly onReady: () => void;
  private readonly onFrame: (frame: Buffer) => void;
  constructor(onReady: () => void, onFrame: (frame: Buffer) => void) {
    this.onReady = onReady;
    this.onFrame = onFrame;
  }
  push(chunk: Buffer): void {
    if (this.closed || !Buffer.isBuffer(chunk) || chunk.length > 65536) throw failure();
    this.buffer = Buffer.concat([this.buffer, chunk]);
    if (!this.ready) {
      if (this.buffer.length < sessionBytes) return;
      if (
        !this.buffer.subarray(0, 8).equals(preamble) ||
        this.buffer.readUInt32LE(8) !== 48000 ||
        this.buffer.readUInt32LE(12) !== 2 ||
        this.buffer.readUInt32LE(16) !== 1 ||
        this.buffer.readUInt32LE(20) !== maxFrames
      )
        throw failure();
      this.buffer = this.buffer.subarray(sessionBytes);
      this.ready = true;
      this.onReady();
    }
    while (this.buffer.length >= frameHeaderBytes) {
      const sequence = this.buffer.readUInt32LE(4);
      const frames = this.buffer.readUInt32LE(8);
      const flags = this.buffer.readUInt32LE(12);
      if (this.buffer.readUInt32LE(0) !== frameMagic || frames === 0 || frames > maxFrames || flags > 1)
        throw failure();
      const bytes = frameHeaderBytes + frames * 8;
      if (this.buffer.length < bytes) return;
      if (this.sequence !== undefined) {
        const distance = (sequence - this.sequence) >>> 0;
        if (distance === 0 || distance >= 0x80000000 || (distance !== 1 && flags !== 1)) throw failure();
      }
      const frame = Buffer.from(this.buffer.subarray(0, bytes));
      for (let offset = frameHeaderBytes; offset < bytes; offset += 4) {
        const sample = frame.readFloatLE(offset);
        if (!Number.isFinite(sample) || Math.abs(sample) > 1) throw failure();
      }
      this.sequence = sequence;
      this.buffer = this.buffer.subarray(bytes);
      this.onFrame(frame);
    }
  }
  close(): void {
    this.closed = true;
    this.buffer = Buffer.alloc(0);
  }
}
