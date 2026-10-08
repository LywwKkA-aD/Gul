import type { Preferences } from './preferences.ts';
import { voiceSettings, type VoiceSettings } from './media/voice-gate.ts';

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
    patch: PreferencePatch,
    current: () => Preferences,
    apply: (next: Preferences, fields: PreferencePatch) => Promise<void>,
  ): Promise<void> {
    const { voice, ...fields } = patch;
    // Preserve explicit intent, including returning a slider to its saved value
    // while an earlier value is still pending. Merge with current state at apply time.
    const change: PreferencePatch = Object.freeze({
      ...fields,
      ...(voice ? { voice: Object.freeze({ ...voice }) } : {}),
    });
    const operation = this.pending.then(() => apply(mergePreferences(current(), change), change));
    this.pending = operation.catch(() => {});
    return operation;
  }
}
