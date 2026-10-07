import { AudioPresets, Track, type LocalAudioTrack, type Room } from 'livekit-client';
import type { AudioState } from '../../shared/contracts.ts';
import { microphone, voiceCaptureOptions } from './capture.ts';
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
    failure: () => void,
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
  private configuration = Promise.resolve();
  private readonly configuring = new WeakMap<LocalAudioTrack, number>();
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
    let capturedSettings = this.preferences;
    try {
      track = await (this.dependencies.capture ?? microphone)(device, capturedSettings);
      if (!valid()) {
        track.stop();
        return;
      }
      this.track = track;
      this.ready = false;
      track.mediaStreamTrack.enabled = false;
      await track.mute();
      capturedSettings = await this.reconcile(track, capturedSettings, valid);
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
        const enabled = !this.configuring.has(track!) && !this.state.muted && !this.state.deafened;
        this.dependencies.reading({
          level: enabled ? reading.level : 0,
          active: enabled && reading.active,
          available: Boolean(this.processor),
        });
      };
      const processingSettings = this.preferences;
      try {
        processor = await (this.dependencies.processor ?? attachVoiceProcessor)(
          track,
          processingSettings,
          report,
          () => {
            if (!valid() || this.track !== track) return;
            void this.stop().catch(() => {});
            this.dependencies.warning('Обработка микрофона прервалась. Подключитесь к каналу заново.', true);
            this.dependencies.reading({ level: 0, active: false, available: false });
          },
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
      if (processor?.failed) throw new Error();
      capturedSettings = await this.reconcile(track, capturedSettings, valid, processor);
      if (!valid()) {
        track.stop();
        await processor?.destroy();
        await unpublish(room, track);
        return;
      }
      if (!processor && (this.preferences.mode !== 'continuous' || this.preferences.inputGain !== 1))
        throw new Error();
      if (processingSettings !== this.preferences) processor?.update(this.preferences);
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
    const enabled =
      this.ready && !state.muted && !state.deafened && (!this.track || !this.configuring.has(this.track));
    this.processor?.setMuted?.(!enabled);
    if (this.track) this.track.mediaStreamTrack.enabled = enabled;
  }
  async synchronize(state: AudioState): Promise<void> {
    this.apply(state);
    const track = this.track;
    if (!track || !this.ready || this.configuring.has(track)) return;
    try {
      await (state.muted || state.deafened ? track.mute() : track.unmute());
    } finally {
      if (this.track === track) this.apply(this.state);
    }
  }
  configure(patch: Partial<VoiceSettings>): Promise<void> {
    const operation = this.configuration.then(() => this.configureNow(patch));
    this.configuration = operation.catch(() => {});
    return operation;
  }
  private async configureNow(patch: Partial<VoiceSettings>): Promise<void> {
    const previous = this.preferences;
    const next = voiceSettings(previous, patch);
    const track = this.track;
    if (track && this.ready && !this.processor && (next.mode !== 'continuous' || next.inputGain !== 1))
      throw new Error('Обработка микрофона недоступна.');
    const revision = ++this.settingsRevision;
    this.preferences = next;
    const flagsChanged =
      next.echoCancellation !== previous.echoCancellation ||
      next.noiseSuppression !== previous.noiseSuppression ||
      next.autoGainControl !== previous.autoGainControl;
    const restart = Boolean(flagsChanged && track && this.ready);
    if (restart && track) {
      this.configuring.set(track, (this.configuring.get(track) ?? 0) + 1);
      this.apply(this.state);
    }
    try {
      if (restart && track) {
        // Chromium can accept applyConstraints without changing active NS/AGC/AEC.
        // Restart the SDK-owned raw capture and its processor while both remain muted.
        await track.mute();
        if (this.track !== track || !this.ready) return;
        this.processor?.update(next);
        await track.restartTrack(voiceCaptureOptions(next, this.device ?? 'default'));
        if (this.track !== track || !this.ready) {
          track.stop();
          return;
        }
      }
      if (this.settingsRevision !== revision) return;
      if (!restart) this.processor?.update(next);
    } catch {
      if (restart && track && this.track !== track) {
        track.stop();
        return;
      }
      if (this.settingsRevision === revision) {
        this.preferences = previous;
        this.processor?.update(previous);
        if (restart && track && this.track === track && this.ready) {
          try {
            await track.restartTrack(voiceCaptureOptions(previous, this.device ?? 'default'));
            if (this.track !== track) track.stop();
          } catch {
            if (this.track === track) {
              await this.stop();
              this.dependencies.warning(
                'Не удалось восстановить микрофон. Подключитесь к каналу заново.',
                true,
              );
            }
          }
        }
      }
      throw new Error('Не удалось изменить настройки микрофона.');
    } finally {
      if (restart && track) {
        const remaining = (this.configuring.get(track) ?? 1) - 1;
        if (remaining) this.configuring.set(track, remaining);
        else this.configuring.delete(track);
        if (this.track === track) await this.synchronize(this.state);
      } else this.apply(this.state);
    }
  }
  private async reconcile(
    track: LocalAudioTrack,
    captured: VoiceSettings,
    current: () => boolean,
    processor?: VoiceProcessorHandle,
  ): Promise<VoiceSettings> {
    while (
      current() &&
      (captured.echoCancellation !== this.preferences.echoCancellation ||
        captured.noiseSuppression !== this.preferences.noiseSuppression ||
        captured.autoGainControl !== this.preferences.autoGainControl)
    ) {
      const next = this.preferences;
      processor?.update(next);
      await track.restartTrack(voiceCaptureOptions(next, this.device ?? 'default'));
      if (!current()) {
        track.stop();
        return next;
      }
      captured = next;
    }
    return captured;
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
