import { defaultVoiceSettings, voiceSettings, type VoiceSettings } from './media/voice-gate.ts';
import { defaultScreenQuality, parseScreenQuality, type ScreenQuality } from './media/screen-settings.ts';
export interface Preferences {
  readonly audioinput: string;
  readonly audiooutput: string;
  readonly shortcut: string;
  readonly toggleEnabled: boolean;
  readonly voice: VoiceSettings;
  readonly soundNotifications: boolean;
  readonly hotkeyMode: 'toggle' | 'hold';
  readonly screenQuality: ScreenQuality;
}

const defaults: Preferences = Object.freeze({
  audioinput: 'default',
  audiooutput: 'default',
  shortcut: 'F8',
  toggleEnabled: false,
  voice: defaultVoiceSettings,
  soundNotifications: false,
  hotkeyMode: 'toggle',
  screenQuality: defaultScreenQuality,
});

export function readSavedString(key: 'gul.address' | 'gul.username'): string {
  try {
    return localStorage.getItem(key) ?? '';
  } catch {
    return '';
  }
}

export function saveConnection(address: string, username: string): void {
  try {
    localStorage.setItem('gul.address', address);
    localStorage.setItem('gul.username', username);
  } catch {
    // Connection is usable when local preference storage is unavailable.
  }
}

export function readPreferences(): Preferences {
  try {
    const input: unknown = JSON.parse(localStorage.getItem('gul.preferences') ?? '{}');
    if (!input || typeof input !== 'object') return defaults;
    const value = input as Record<string, unknown>;
    const device = (kind: 'audioinput' | 'audiooutput') =>
      typeof value[kind] === 'string' && value[kind].length > 0 && value[kind].length <= 512
        ? value[kind]
        : 'default';
    const shortcut =
      typeof value.shortcut === 'string' &&
      value.shortcut.length > 0 &&
      value.shortcut.length <= 96 &&
      !/[\u0000-\u001f]/u.test(value.shortcut)
        ? value.shortcut
        : defaults.shortcut;
    return Object.freeze({
      audioinput: device('audioinput'),
      audiooutput: device('audiooutput'),
      shortcut,
      toggleEnabled: value.toggleEnabled === true,
      voice: readVoice(value.voice),
      soundNotifications: value.soundNotifications === true,
      hotkeyMode: value.hotkeyMode === 'hold' ? 'hold' : 'toggle',
      screenQuality: parseScreenQuality(value.screenQuality),
    });
  } catch {
    return defaults;
  }
}
function readVoice(value: unknown): VoiceSettings {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return defaultVoiceSettings;
  const input = value as Record<string, unknown>;
  const fields = Object.fromEntries(
    Object.keys(defaultVoiceSettings)
      .filter((key) => Object.hasOwn(input, key))
      .map((key) => [key, input[key]]),
  );
  try {
    return voiceSettings(defaultVoiceSettings, fields as Partial<VoiceSettings>);
  } catch {
    return defaultVoiceSettings;
  }
}

export function savePreferences(preferences: Preferences): void {
  try {
    localStorage.setItem('gul.preferences', JSON.stringify(preferences));
  } catch {
    // Settings still apply for this running client.
  }
}

/** Electron accelerator from a deliberate key capture, without free-form script input. */
export function shortcutFromKey(event: KeyboardEvent): string | undefined {
  const code = event.code;
  const named: Record<string, string> = {
    Space: 'Space',
    ArrowUp: 'Up',
    ArrowDown: 'Down',
    ArrowLeft: 'Left',
    ArrowRight: 'Right',
    Enter: 'Return',
    Backspace: 'Backspace',
    Delete: 'Delete',
    Home: 'Home',
    End: 'End',
    PageUp: 'PageUp',
    PageDown: 'PageDown',
  };
  const key = /^Key[A-Z]$/u.test(code)
    ? code.slice(3)
    : /^Digit[0-9]$/u.test(code)
      ? code.slice(5)
      : /^F([1-9]|1[0-9]|2[0-4])$/u.test(code)
        ? code
        : named[code];
  if (!key) return undefined;
  return [
    ...(event.ctrlKey ? ['Control'] : []),
    ...(event.altKey ? ['Alt'] : []),
    ...(event.shiftKey ? ['Shift'] : []),
    ...(event.metaKey ? ['Super'] : []),
    key,
  ].join('+');
}
