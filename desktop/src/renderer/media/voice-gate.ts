export interface VoiceSettings {
  readonly mode: 'continuous' | 'vad';
  readonly thresholdDb: number;
  readonly holdMs: number;
  readonly inputGain: number;
  readonly echoCancellation: boolean;
  readonly noiseSuppression: boolean;
  readonly autoGainControl: boolean;
}
export const defaultVoiceSettings: VoiceSettings = Object.freeze({
  mode: 'continuous',
  thresholdDb: -45,
  holdMs: 250,
  inputGain: 1,
  echoCancellation: true,
  noiseSuppression: true,
  autoGainControl: true,
});
export function voiceSettings(previous: VoiceSettings, patch: Partial<VoiceSettings>): VoiceSettings {
  const next = { ...previous, ...patch };
  if (
    (next.mode !== 'continuous' && next.mode !== 'vad') ||
    !Number.isFinite(next.thresholdDb) ||
    next.thresholdDb < -80 ||
    next.thresholdDb > -6 ||
    !Number.isFinite(next.holdMs) ||
    next.holdMs < 0 ||
    next.holdMs > 1000 ||
    !Number.isFinite(next.inputGain) ||
    next.inputGain < 0 ||
    next.inputGain > 2 ||
    !['echoCancellation', 'noiseSuppression', 'autoGainControl'].every(
      (key) => typeof next[key as keyof VoiceSettings] === 'boolean',
    )
  )
    throw new Error('Недействительные настройки микрофона.');
  return Object.freeze(next);
}
export interface VoiceReading {
  readonly level: number;
  readonly active: boolean;
  readonly gain: number;
}

/** RMS gate after Chromium speech processing. Gain is applied once by the audio worklet. */
export class VoiceGate {
  private settings: VoiceSettings;
  private lastSpeech = Number.NEGATIVE_INFINITY;
  private lastTime = Number.NEGATIVE_INFINITY;
  constructor(settings: VoiceSettings = defaultVoiceSettings) {
    this.settings = voiceSettings(defaultVoiceSettings, settings);
  }
  update(settings: VoiceSettings): void {
    const next = voiceSettings(defaultVoiceSettings, settings);
    if (next.mode !== this.settings.mode || next.thresholdDb !== this.settings.thresholdDb)
      this.lastSpeech = Number.NEGATIVE_INFINITY;
    this.settings = next;
  }
  read(samples: Float32Array, nowMs: number): VoiceReading {
    if (!Number.isFinite(nowMs) || !samples.length || samples.some((sample) => !Number.isFinite(sample)))
      return { level: 0, active: false, gain: 0 };
    if (nowMs < this.lastTime) this.lastSpeech = Number.NEGATIVE_INFINITY;
    this.lastTime = nowMs;
    const energy = samples.reduce((sum, sample) => sum + sample * sample, 0) / samples.length;
    const level = Math.min(1, Math.sqrt(energy) * this.settings.inputGain);
    const speech = level > 0 && 20 * Math.log10(level) >= this.settings.thresholdDb;
    if (speech) this.lastSpeech = nowMs;
    const active = speech || nowMs - this.lastSpeech <= this.settings.holdMs;
    return {
      level,
      active,
      gain: this.settings.mode === 'continuous' || active ? this.settings.inputGain : 0,
    };
  }
}
