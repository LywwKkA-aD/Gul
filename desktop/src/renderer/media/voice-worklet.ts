import { defaultVoiceSettings, VoiceGate, voiceSettings, type VoiceSettings } from './voice-gate.ts';

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
  constructor(options: { processorOptions?: VoiceSettings }) {
    super();
    this.gate = new VoiceGate(voiceSettings(defaultVoiceSettings, options.processorOptions ?? {}));
    this.port.onmessage = ({ data }: MessageEvent<unknown>) => {
      if (!data || typeof data !== 'object') return;
      const value = data as Record<string, unknown>;
      if (value.type !== 'settings' || !value.settings || typeof value.settings !== 'object') return;
      try {
        this.gate.update(voiceSettings(defaultVoiceSettings, value.settings as Partial<VoiceSettings>));
      } catch {
        /* Only validated settings can change the audio graph. */
      }
    };
  }
  process(inputs: readonly Float32Array[][], outputs: readonly Float32Array[][]): boolean {
    const input = inputs[0]?.[0] ?? this.silence;
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
