const capacity = 5760;

/** Fixed stereo 48 kHz with 20 ms startup and at most 120 ms queued audio. */
export class ScreenPCM {
  private readonly samples = new Float32Array(capacity * 2);
  private cursor = 0;
  private queued = 0;
  private primed = false;
  private sequence?: number;
  push(buffer: ArrayBuffer): void {
    if (!(buffer instanceof ArrayBuffer) || buffer.byteLength < 24 || buffer.byteLength > 3856)
      throw new Error('GUL_SCREEN_AUDIO_FRAME');
    const data = new DataView(buffer);
    const sequence = data.getUint32(4, true);
    const frames = data.getUint32(8, true);
    const flags = data.getUint32(12, true);
    if (
      data.getUint32(0, true) !== 0x314c5547 ||
      frames < 1 ||
      frames > 480 ||
      flags > 1 ||
      buffer.byteLength !== 16 + frames * 8
    )
      throw new Error('GUL_SCREEN_AUDIO_FRAME');
    if (this.sequence !== undefined) {
      const distance = (sequence - this.sequence) >>> 0;
      if (distance === 0 || distance >= 0x80000000 || (distance !== 1 && !flags))
        throw new Error('GUL_SCREEN_AUDIO_FRAME');
    }
    for (let offset = 16; offset < buffer.byteLength; offset += 4) {
      const sample = data.getFloat32(offset, true);
      if (!Number.isFinite(sample) || Math.abs(sample) > 1) throw new Error('GUL_SCREEN_AUDIO_FRAME');
    }
    if (flags) {
      this.queued = 0;
      this.primed = false;
    }
    if (this.queued + frames > capacity) throw new Error('GUL_SCREEN_AUDIO_BUFFER');
    for (let frame = 0; frame < frames; frame++) {
      const index = ((this.cursor + this.queued + frame) % capacity) * 2;
      this.samples[index] = data.getFloat32(16 + frame * 8, true);
      this.samples[index + 1] = data.getFloat32(20 + frame * 8, true);
    }
    this.queued += frames;
    this.sequence = sequence;
  }
  read(left: Float32Array, right: Float32Array): void {
    left.fill(0);
    right.fill(0);
    if (left.length !== right.length || left.length > 480) throw new Error('GUL_SCREEN_AUDIO_OUTPUT');
    if (!this.primed) {
      if (this.queued < 960) return;
      this.primed = true;
    }
    const frames = Math.min(left.length, this.queued);
    for (let frame = 0; frame < frames; frame++) {
      const index = ((this.cursor + frame) % capacity) * 2;
      left[frame] = this.samples[index];
      right[frame] = this.samples[index + 1];
    }
    this.cursor = (this.cursor + frames) % capacity;
    this.queued -= frames;
    if (frames < left.length) this.primed = false;
  }
}
