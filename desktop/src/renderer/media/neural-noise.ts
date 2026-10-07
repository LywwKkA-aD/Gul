export interface NoiseRuntime {
  readonly HEAPF32: Float32Array;
  readonly _rnnoise_create: (model: number) => number;
  readonly _rnnoise_process_frame: (state: number, output: number, input: number) => number;
  readonly _rnnoise_destroy: (state: number) => void;
  readonly _malloc: (bytes: number) => number;
  readonly _free: (pointer: number) => void;
}

/** RNNoise's 10ms frames bridge Chromium's 128-sample render blocks. Buffers and
 * WASM state are allocated once, and microphone PCM stays on the audio thread.
 */
export class NeuralDenoiser {
  private readonly runtime: NoiseRuntime;
  private readonly state: number;
  private readonly pointer: number;
  private readonly input = new Float32Array(480);
  private readonly filtered = new Float32Array(480);
  private inputIndex = 0;
  private outputIndex = 0;
  private available = 0;
  private closed = false;
  constructor(runtime: NoiseRuntime) {
    this.runtime = runtime;
    this.state = runtime._rnnoise_create(0);
    if (!this.state) throw new Error('Unavailable noise processing.');
    this.pointer = runtime._malloc(480 * 4);
    if (!this.pointer) {
      runtime._rnnoise_destroy(this.state);
      throw new Error('Unavailable noise processing.');
    }
  }
  process(input: Float32Array, output: Float32Array): void {
    if (this.closed) {
      output.fill(0);
      return;
    }
    for (let index = 0; index < output.length; index++) {
      const sample = input[index] ?? 0;
      this.input[this.inputIndex++] = Number.isFinite(sample) ? Math.max(-1, Math.min(1, sample)) * 32768 : 0;
      if (this.inputIndex === 480) {
        const offset = this.pointer / 4;
        this.runtime.HEAPF32.set(this.input, offset);
        this.runtime._rnnoise_process_frame(this.state, this.pointer, this.pointer);
        const heap = this.runtime.HEAPF32;
        for (let frame = 0; frame < 480; frame++) {
          const value = heap[offset + frame] / 32768;
          this.filtered[frame] = Number.isFinite(value) ? Math.max(-1, Math.min(1, value)) : 0;
        }
        this.inputIndex = 0;
        this.outputIndex = 0;
        this.available = 480;
      }
      output[index] = this.available ? this.filtered[this.outputIndex++] : 0;
      if (this.available) this.available--;
    }
  }
  destroy(): void {
    if (this.closed) return;
    this.closed = true;
    this.runtime._free(this.pointer);
    this.runtime._rnnoise_destroy(this.state);
    this.input.fill(0);
    this.filtered.fill(0);
  }
}
