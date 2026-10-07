import type { LocalAudioTrack, LocalVideoTrack, Room, Track } from 'livekit-client';
import type { AudioState, MediaGrant } from '../../shared/contracts.ts';
import { defaultVoiceSettings, type VoiceSettings, type VoiceReading } from './voice-gate.ts';
import type { VoiceProcessorHandle } from './voice-processor.ts';

export interface ParticipantInfo {
  readonly identity: string;
  readonly name: string;
}
export interface ScreenInfo {
  readonly identity: string;
  readonly ownerIdentity: string;
  readonly name: string;
  readonly videoSid: string;
  readonly audioSid?: string;
  readonly watching: boolean;
  readonly local: boolean;
  readonly state: 'available' | 'watching' | 'paused';
}
export interface VideoInfo {
  readonly id: string;
  readonly identity: string;
  readonly local: boolean;
  readonly track: Track;
}
export interface ChatEntry {
  readonly id: number;
  readonly identity: string;
  readonly name: string;
  readonly text: string;
  readonly local: boolean;
  readonly time: number;
}
export interface Snapshot extends AudioState {
  readonly state: 'disconnected' | 'connecting' | 'connected' | 'reconnecting';
  readonly error: string;
  readonly warning: string;
  readonly participants: readonly ParticipantInfo[];
  readonly screens: readonly ScreenInfo[];
  readonly videos: readonly VideoInfo[];
  readonly chat: readonly ChatEntry[];
  readonly speakers: readonly string[];
  readonly pingMs: number | null;
  readonly sharing: boolean;
  readonly pendingShare: boolean;
  readonly screenAudio: 'off' | 'capturing' | 'unavailable';
  readonly micLevel: number;
  readonly voiceActive: boolean;
  readonly voiceSettings: VoiceSettings;
  readonly voiceProcessingAvailable: boolean;
}
export interface ScreenCapture {
  readonly tracks: readonly (LocalVideoTrack | LocalAudioTrack)[];
  readonly cleanup?: () => void;
}
export interface Dependencies {
  readonly screenGrant: () => Promise<MediaGrant>;
  readonly audioState: (state: AudioState) => Promise<AudioState>;
  readonly roomFactory?: (kind: 'voice' | 'screen') => Room;
  readonly micFactory?: (deviceId?: string, settings?: VoiceSettings) => Promise<LocalAudioTrack>;
  readonly voiceProcessorFactory?: (
    track: LocalAudioTrack,
    settings: VoiceSettings,
    reading: (reading: Pick<VoiceReading, 'level' | 'active'>) => void,
    failure: () => void,
  ) => Promise<VoiceProcessorHandle | undefined>;
  readonly captureFactory?: (withAudio: boolean) => Promise<ScreenCapture>;
  readonly audioElementFactory?: () => HTMLAudioElement;
  /** Use only after this machine's actual WebRTC H.264 encoder was measured. */
  readonly preferH264?: () => Promise<boolean>;
  readonly now?: () => number;
}
export function initialSnapshot(): Snapshot {
  return Object.freeze({
    state: 'disconnected',
    error: '',
    warning: '',
    participants: Object.freeze([]),
    screens: Object.freeze([]),
    videos: Object.freeze([]),
    chat: Object.freeze([]),
    speakers: Object.freeze([]),
    pingMs: null,
    sharing: false,
    pendingShare: false,
    screenAudio: 'off',
    micLevel: 0,
    voiceActive: false,
    voiceSettings: defaultVoiceSettings,
    voiceProcessingAvailable: false,
    muted: false,
    deafened: false,
  });
}
