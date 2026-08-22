// Transmit gate model shared by the settings UI and the push-to-talk
// listener. The defaults mirror internal/audio (open 0.6, hangover 300 ms), so
// a freshly started app and a freshly started engine agree without an initial
// round trip. Nothing here is persisted: the settings live on the engine and
// survive its restarts, but not an application restart - config.json is M4.

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

/** Width of the hysteresis band. One slider drives both edges: the closing
    threshold is an implementation detail of the gate, not a second decision
    to put in front of the user. */
const VAD_HYSTERESIS = 0.2;

/** Lower bound for the closing edge. Reachable only if the open threshold
    range ever drops below 0.25. */
const VAD_CLOSE_FLOOR = 0.05;

/** Closing edge that trails the given open threshold. Rounded to whole
    percent so the value that reaches Go carries no float noise. */
export function closeThreshold(open: number): number {
  return Math.max(VAD_CLOSE_FLOOR, Math.round((open - VAD_HYSTERESIS) * 100) / 100);
}

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
