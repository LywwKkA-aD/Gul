import { defaultVoiceSettings, VoiceGate, voiceSettings, type VoiceSettings } from './voice-gate.ts';
import { createNeuralDenoiser } from './neural-runtime.ts';
import type { NeuralDenoiser } from './neural-noise.ts';

declare const currentFrame: number;
declare const sampleRate: number;
declare class AudioWorkletProcessor {
  readonly port: MessagePort;
  constructor(options?: unknown);
}
declare function registerProcessor(name: string, processor: typeof AudioWorkletProcessor): void;

/** Audio-thread RMS gate; bounded 20Hz meter messages never carry PCM into the UI or main process. */
class GulVoiceWorklet extends AudioWorkletProcessor {
  private readonly gate: VoiceGate;
  private readonly silence = new Float32Array(128);
  private lastReport = Number.NEGATIVE_INFINITY;
  private smoothedGain = 0;
  private denoiser?: NeuralDenoiser;
  private readonly filtered = new Float32Array(128);
  private closed = false;
  constructor(options: { processorOptions?: VoiceSettings }) {
    super();
    const settings = voiceSettings(defaultVoiceSettings, options.processorOptions ?? {});
    this.gate = new VoiceGate(settings);
    this.suppression(settings.noiseSuppression);
    this.port.onmessage = ({ data }: MessageEvent<unknown>) => {
      if (this.closed) return;
      if (!data || typeof data !== 'object') return;
      const value = data as Record<string, unknown>;
      if (value.type === 'destroy') {
        this.closed = true;
        this.denoiser?.destroy();
        this.denoiser = undefined;
        return;
      }
      if (value.type !== 'settings' || !value.settings || typeof value.settings !== 'object') return;
      try {
        const settings = voiceSettings(defaultVoiceSettings, value.settings as Partial<VoiceSettings>);
        this.suppression(settings.noiseSuppression);
        this.gate.update(settings);
      } catch {
        /* Only validated settings can change the audio graph. */
      }
    };
    this.port.postMessage({ type: 'ready', neuralNoise: Boolean(this.denoiser), sampleRate });
  }
  private suppression(enabled: boolean): void {
    if (enabled && sampleRate === 48000) this.denoiser ??= createNeuralDenoiser();
    else {
      this.denoiser?.destroy();
      this.denoiser = undefined;
    }
  }
  process(inputs: readonly Float32Array[][], outputs: readonly Float32Array[][]): boolean {
    if (this.closed) {
      outputs[0]?.[0]?.fill(0);
      return false;
    }
    const captured = inputs[0]?.[0] ?? this.silence;
    let input = captured;
    if (this.denoiser) {
      this.denoiser.process(captured, this.filtered);
      input = this.filtered;
    }
    const reading = this.gate.read(input, (currentFrame * 1000) / sampleRate);
    const output = outputs[0]?.[0];
    if (output) {
      const smoothing = 1 / Math.max(1, sampleRate * 0.003);
      for (let index = 0; index < output.length; index++) {
        this.smoothedGain += (reading.gain - this.smoothedGain) * smoothing;
        const sample = input[index] ?? 0;
        output[index] = Number.isFinite(sample) ? sample * this.smoothedGain : 0;
      }
    }
    if (currentFrame - this.lastReport >= sampleRate / 20) {
      this.lastReport = currentFrame;
      this.port.postMessage({ type: 'level', level: reading.level, active: reading.active });
    }
    return true;
  }
}
registerProcessor('gul-voice', GulVoiceWorklet);
