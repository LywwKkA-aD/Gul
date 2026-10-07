import { ScreenPCM } from './screen-pcm.ts';

declare class AudioWorkletProcessor {
  readonly port: MessagePort;
  constructor(options?: unknown);
}
declare function registerProcessor(name: string, processor: typeof AudioWorkletProcessor): void;

/** Desktop audio is stereo PCM; microphone AEC, noise suppression and AGC never touch it. */
class ScreenAudioWorklet extends AudioWorkletProcessor {
  private readonly pcm = new ScreenPCM();
  private failed = false;
  constructor() {
    super();
    this.port.onmessage = ({ data }: MessageEvent<unknown>) => {
      if (this.failed) return;
      try {
        if (!(data instanceof ArrayBuffer)) throw new Error();
        this.pcm.push(data);
      } catch (error) {
        this.failed = true;
        const fault = error instanceof Error ? error.message : '';
        this.port.postMessage(
          fault === 'GUL_SCREEN_AUDIO_BUFFER' || fault === 'GUL_SCREEN_AUDIO_FRAME'
            ? fault
            : 'GUL_SCREEN_AUDIO_UNAVAILABLE',
        );
      }
    };
  }
  process(_inputs: readonly Float32Array[][], outputs: readonly Float32Array[][]): boolean {
    const [left, right] = outputs[0] ?? [];
    if (left && right) this.pcm.read(left, right);
    return !this.failed;
  }
}
registerProcessor('gul-screen-audio', ScreenAudioWorklet);
