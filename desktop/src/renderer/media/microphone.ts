import { AudioPresets, Track, type LocalAudioTrack, type Room } from 'livekit-client';
import type { AudioState } from '../../shared/contracts.ts';
import { microphone } from './capture.ts';
import { unpublish } from './rooms.ts';
import { defaultVoiceSettings, voiceSettings, type VoiceSettings, type VoiceReading } from './voice-gate.ts';
import { attachVoiceProcessor, type VoiceProcessorHandle } from './voice-processor.ts';

export interface MicReading {
  readonly level: number;
  readonly active: boolean;
  readonly available: boolean;
}
export interface MicrophoneDependencies {
  readonly capture?: (deviceId?: string, settings?: VoiceSettings) => Promise<LocalAudioTrack>;
  readonly processor?: (
    track: LocalAudioTrack,
    settings: VoiceSettings,
    reading: (reading: Pick<VoiceReading, 'level' | 'active'>) => void,
  ) => Promise<VoiceProcessorHandle | undefined>;
  readonly warning: (message: string, muted: boolean) => void;
  readonly reading: (reading: MicReading) => void;
}

/** Local audio stays fail-closed through capture, processor setup, device restart and room changes. */
export class Microphone {
  private readonly dependencies: MicrophoneDependencies;
  private track?: LocalAudioTrack;
  private processor?: VoiceProcessorHandle;
  private device?: string;
  private ready = false;
  private opening = false;
  private generation = 0;
  private settingsRevision = 0;
  private state: AudioState = { muted: false, deafened: false };
  private preferences = defaultVoiceSettings;
  constructor(dependencies: MicrophoneDependencies) {
    this.dependencies = dependencies;
  }
  get captured(): boolean {
    return Boolean(this.track || this.opening);
  }
  get settings(): VoiceSettings {
    return this.preferences;
  }
  get processingAvailable(): boolean {
    return Boolean(this.processor);
  }
  get sender(): RTCRtpSender | undefined {
    return this.track?.sender;
  }
  useDevice(device: string | undefined): void {
    this.device = device;
  }

  async start(room: Room, device: string | undefined, current: () => boolean): Promise<void> {
    if (this.captured) return;
    const generation = ++this.generation;
    this.opening = true;
    this.device = device;
    const valid = () => this.generation === generation && current();
    let track: LocalAudioTrack | undefined;
    let processor: VoiceProcessorHandle | undefined;
    let published = false;
    try {
      track = await (this.dependencies.capture ?? microphone)(device, this.preferences);
      if (!valid()) {
        track.stop();
        return;
      }
      this.track = track;
      this.ready = false;
      track.mediaStreamTrack.enabled = false;
      await track.mute();
      if (!valid()) {
        track.stop();
        return;
      }
      await room.localParticipant.publishTrack(track, {
        source: Track.Source.Microphone,
        audioPreset: AudioPresets.speech,
        forceStereo: false,
        dtx: false,
        red: true,
      });
      published = true;
      if (!valid()) {
        track.stop();
        await unpublish(room, track);
        return;
      }
      const report = (reading: Pick<VoiceReading, 'level' | 'active'>) => {
        if (!valid() || !this.ready) return;
        const enabled = !this.state.muted && !this.state.deafened;
        this.dependencies.reading({
          level: enabled ? reading.level : 0,
          active: enabled && reading.active,
          available: Boolean(this.processor),
        });
      };
      try {
        processor = await (this.dependencies.processor ?? attachVoiceProcessor)(
          track,
          this.preferences,
          report,
        );
      } catch {
        if (this.preferences.mode !== 'continuous' || this.preferences.inputGain !== 1) throw new Error();
        if (valid())
          this.dependencies.warning(
            'Обработка микрофона недоступна. Используется стандартный звук Chromium.',
            false,
          );
      }
      if (!valid()) {
        await processor?.destroy();
        track.stop();
        await unpublish(room, track);
        return;
      }
      if (!processor && (this.preferences.mode !== 'continuous' || this.preferences.inputGain !== 1))
        throw new Error();
      this.processor = processor;
      this.ready = true;
      this.dependencies.reading({ level: 0, active: false, available: Boolean(processor) });
      await this.synchronize(this.state);
    } catch {
      await processor?.destroy();
      track?.stop();
      if (published && track) await unpublish(room, track);
      if (valid()) {
        this.track = undefined;
        this.processor = undefined;
        this.ready = false;
        this.dependencies.warning(
          'Не удалось включить микрофон или его обработку. Голос и демонстрации других участников доступны.',
          true,
        );
        this.dependencies.reading({ level: 0, active: false, available: false });
      }
    } finally {
      if (this.generation === generation) this.opening = false;
    }
  }
  apply(state: AudioState): void {
    this.state = Object.freeze({ ...state });
    this.processor?.setMuted?.(!this.ready || state.muted || state.deafened);
    if (this.track) this.track.mediaStreamTrack.enabled = this.ready && !state.muted && !state.deafened;
  }
  async synchronize(state: AudioState): Promise<void> {
    this.apply(state);
    const track = this.track;
    if (!track || !this.ready) return;
    try {
      await (state.muted || state.deafened ? track.mute() : track.unmute());
    } finally {
      if (this.track === track) this.apply(this.state);
    }
  }
  async configure(patch: Partial<VoiceSettings>): Promise<void> {
    const previous = this.preferences;
    const next = voiceSettings(previous, patch);
    const track = this.track;
    if (track && !this.processor && (next.mode !== 'continuous' || next.inputGain !== 1))
      throw new Error('Обработка микрофона недоступна.');
    const revision = ++this.settingsRevision;
    this.preferences = next;
    const flagsChanged =
      next.echoCancellation !== previous.echoCancellation ||
      next.noiseSuppression !== previous.noiseSuppression ||
      next.autoGainControl !== previous.autoGainControl;
    try {
      if (flagsChanged && track && this.ready)
        await track.applyConstraints({
          echoCancellation: next.echoCancellation,
          noiseSuppression: next.noiseSuppression,
          autoGainControl: next.autoGainControl,
        });
      if (this.settingsRevision !== revision) return;
      this.processor?.update(next);
    } catch {
      if (this.settingsRevision === revision) {
        this.preferences = previous;
        this.processor?.update(previous);
      }
      throw new Error('Не удалось изменить настройки микрофона.');
    } finally {
      this.apply(this.state);
    }
  }
  async stop(): Promise<void> {
    this.generation++;
    const track = this.track;
    const processor = this.processor;
    this.track = undefined;
    this.processor = undefined;
    this.ready = false;
    this.opening = false;
    track?.stop();
    await processor?.destroy();
  }
}
