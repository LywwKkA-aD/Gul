// The extension is explicit because node runs this module as it is for the
// unit tests (npm test) and its resolver does not guess one.
import {
  PTT_KEY_DEFAULT,
  VAD_HANGOVER_DEFAULT,
  VAD_OPEN_DEFAULT,
  clampOpenThreshold,
  snapHangoverMs,
} from './gate.ts';
import type { GateMode } from './gate';

// Settings persisted by the Go side (internal/config, config.json) and read
// once at startup through SettingsService. The frontend never stores them: it
// writes every change through a service and gets the snapshot back on the
// next start.

/** The official Gul relay, offered by an explicit action - never prefilled. */
export const GUL_RELAY_ADDRESS = 'wss://murmur.gulvox.com/mumble';

/** The persisted settings as the store holds them. Mirrors services.Settings
    (bindings: services/models.ts). The server password is not part of it: it
    lives in the connect form for exactly one attempt. */
export interface GulSettings {
  address: string;
  username: string;
  captureId: string;
  playbackId: string;
  gateMode: GateMode;
  vadOpen: number;
  hangoverMs: number;
  pttKey: string;
  globalPtt: boolean;
}

export const DEFAULT_SETTINGS: GulSettings = {
  address: '',
  username: '',
  captureId: '',
  playbackId: '',
  gateMode: 'vad',
  vadOpen: VAD_OPEN_DEFAULT,
  hangoverMs: VAD_HANGOVER_DEFAULT,
  pttKey: PTT_KEY_DEFAULT,
  globalPtt: false,
};

function str(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback;
}

/** Folds a snapshot from the backend into the store's shape.
 *
 * Go validates and clamps before anything is written, so this is not a second
 * opinion on the document: it covers a snapshot that never arrived (the call
 * failed, the bindings are from another build) and the ranges this UI offers,
 * which are narrower than the ones the engine accepts. */
export function normalizeSettings(raw: unknown): GulSettings {
  if (typeof raw !== 'object' || raw === null) return DEFAULT_SETTINGS;
  const s = raw as Partial<Record<keyof GulSettings, unknown>>;

  return {
    address: str(s.address, DEFAULT_SETTINGS.address),
    username: str(s.username, DEFAULT_SETTINGS.username),
    captureId: str(s.captureId, DEFAULT_SETTINGS.captureId),
    playbackId: str(s.playbackId, DEFAULT_SETTINGS.playbackId),
    // An unknown mode is the one field with no safe guess: voice activation
    // is the default the engine also falls back to.
    gateMode: s.gateMode === 'ptt' ? 'ptt' : 'vad',
    vadOpen: clampOpenThreshold(typeof s.vadOpen === 'number' ? s.vadOpen : NaN),
    hangoverMs: snapHangoverMs(typeof s.hangoverMs === 'number' ? s.hangoverMs : NaN),
    pttKey: str(s.pttKey, DEFAULT_SETTINGS.pttKey) || DEFAULT_SETTINGS.pttKey,
    globalPtt: s.globalPtt === true,
  };
}
