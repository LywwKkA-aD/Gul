// Transmit gate model shared by the settings UI and the push-to-talk
// listener. The defaults mirror internal/config and internal/audio (open 0.6,
// hangover 300 ms), so a freshly started app and a freshly started engine
// agree without an initial round trip. The values themselves are persisted by
// the Go side (config.json); this module only shapes what the UI offers.

/** What opens the microphone. Matches the strings core.ParseGateMode accepts. */
export type GateMode = 'vad' | 'ptt';

/** Open threshold, as an RNNoise speech probability. Below 0.30 a fan or a
    keyboard opens the gate; above 0.95 quiet speech never does. */
export const VAD_OPEN_MIN = 0.3;
export const VAD_OPEN_MAX = 0.95;
export const VAD_OPEN_DEFAULT = 0.6;

/** Hangover tail in ms: how long a transmission survives a gap inside a
    phrase. Below 100 ms the gate chops unvoiced consonants; above 1000 ms it
    carries the whole pause between phrases. */
export const VAD_HANGOVER_MIN = 100;
export const VAD_HANGOVER_MAX = 1000;
export const VAD_HANGOVER_DEFAULT = 300;

/** Offered tail lengths, in ms. */
export const VAD_HANGOVER_CHOICES = [100, 200, 300, 500, 700, 1000] as const;

/** KeyboardEvent.code of the default push-to-talk key. */
export const PTT_KEY_DEFAULT = 'Space';

/** Folds an open threshold into the offered range; NaN falls back to default. */
export function clampOpenThreshold(open: number): number {
  if (!Number.isFinite(open)) return VAD_OPEN_DEFAULT;
  return Math.min(VAD_OPEN_MAX, Math.max(VAD_OPEN_MIN, open));
}

/** Folds a hangover into the offered range; NaN falls back to default. */
export function clampHangoverMs(ms: number): number {
  if (!Number.isFinite(ms)) return VAD_HANGOVER_DEFAULT;
  return Math.round(Math.min(VAD_HANGOVER_MAX, Math.max(VAD_HANGOVER_MIN, ms)));
}

/** Nearest offered tail to a stored one. A hand-edited config.json may hold a
    tail this UI does not offer, and a select with no matching option shows
    nothing at all. */
export function snapHangoverMs(ms: number): number {
  const clamped = clampHangoverMs(ms);
  let nearest: number = VAD_HANGOVER_DEFAULT;
  let distance = Number.POSITIVE_INFINITY;
  for (const choice of VAD_HANGOVER_CHOICES) {
    const gap = Math.abs(choice - clamped);
    if (gap < distance) {
      nearest = choice;
      distance = gap;
    }
  }
  return nearest;
}

/* KeyboardEvent.code is layout independent - the stored key stays the same
   physical key when the user switches to a Cyrillic layout - but it is not
   readable, so it is rendered through this table. */
const KEY_LABELS: Record<string, string> = {
  Space: 'Пробел',
  Enter: 'Enter',
  NumpadEnter: 'Num Enter',
  Tab: 'Tab',
  Backspace: 'Backspace',
  CapsLock: 'Caps Lock',
  ShiftLeft: 'Shift слева',
  ShiftRight: 'Shift справа',
  ControlLeft: 'Ctrl слева',
  ControlRight: 'Ctrl справа',
  AltLeft: 'Alt слева',
  AltRight: 'Alt справа',
  MetaLeft: 'Meta слева',
  MetaRight: 'Meta справа',
  ArrowUp: 'Стрелка вверх',
  ArrowDown: 'Стрелка вниз',
  ArrowLeft: 'Стрелка влево',
  ArrowRight: 'Стрелка вправо',
  Insert: 'Insert',
  Delete: 'Delete',
  Home: 'Home',
  End: 'End',
  PageUp: 'Page Up',
  PageDown: 'Page Down',
  Backquote: '`',
  Minus: '-',
  Equal: '=',
  BracketLeft: '[',
  BracketRight: ']',
  Backslash: '\\',
  Semicolon: ';',
  Quote: "'",
  Comma: ',',
  Period: '.',
  Slash: '/',
};

/** Human-readable name of a physical key. */
export function keyLabel(code: string): string {
  const named = KEY_LABELS[code];
  if (named) return named;
  if (/^Key[A-Z]$/.test(code)) return code.slice(3);
  if (/^Digit\d$/.test(code)) return code.slice(5);
  if (/^Numpad\S+$/.test(code)) return `Num ${code.slice(6)}`;
  return code;
}
