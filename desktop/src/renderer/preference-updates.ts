import type { Preferences } from './preferences.ts';
import { defaultVoiceSettings, voiceSettings, type VoiceSettings } from './media/voice-gate.ts';

export type PreferencePatch = Omit<Partial<Preferences>, 'voice'> & {
  readonly voice?: Partial<VoiceSettings>;
};
export function mergePreferences(current: Preferences, patch: PreferencePatch): Preferences {
  const { voice, ...fields } = patch;
  return Object.freeze({
    ...current,
    ...fields,
    voice: voice ? voiceSettings(current.voice, voice) : current.voice,
  });
}

/** Commit only successful fields and serialize device changes across reopened dialogs. */
export class PreferenceUpdateQueue {
  private pending = Promise.resolve();
  run(
    patch: Partial<Preferences>,
    current: () => Preferences,
    apply: (next: Preferences, fields: PreferencePatch) => Promise<void>,
  ): Promise<void> {
    const { voice, ...fields } = patch;
    const before = current();
    // Settings sends a complete voice snapshot. Carry only the deliberate changes
    // across the queue so an older render cannot revert another pending setting.
    const changedVoice = voice
      ? Object.fromEntries(
          (Object.keys(defaultVoiceSettings) as (keyof VoiceSettings)[])
            .filter((key) => voice[key] !== before.voice[key])
            .map((key) => [key, voice[key]]),
        )
      : undefined;
    const change: PreferencePatch = Object.freeze({
      ...fields,
      ...(changedVoice ? { voice: Object.freeze(changedVoice) } : {}),
    });
    const operation = this.pending.then(() => apply(mergePreferences(current(), change), change));
    this.pending = operation.catch(() => {});
    return operation;
  }
}
