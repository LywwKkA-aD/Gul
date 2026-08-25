// The connect screen's server picker, minus React.
//
// Two decisions live here and nowhere else: what one remembered server looks
// like as a row, and what the screen does when a click on one does not start a
// connect. Both are pure, so both are tested (savedServers.test.ts).
//
// The extension on the import is explicit because node runs this module as it
// is for the unit tests and its resolver does not guess one.
import { serverHost } from './serverLabel.ts';

/** One remembered server, ready to draw. */
export interface SavedServerRow {
  /** The dial string, and the identity: it is the key the Go side stores the
      password under, so it is never folded or rewritten. */
  address: string;
  username: string;
  hasPassword: boolean;
  /** What the row shows first: the host, which is the part a person
      recognises. Falls back to the address when it cannot be reduced. */
  label: string;
  /** Russian, under the name; empty when there is nothing to say. */
  hint: string;
}

/** What the screen shows next to a saved row. A stored password is the whole
    reason a click is enough, so it is the one thing worth a word. */
const PASSWORD_HINT = 'пароль сохранён';

/** Said when a click failed for a reason this build does not know. Better a
    vague sentence than a silent dead row. */
const UNKNOWN_FAILURE = 'Не удалось подключиться к сохранённому серверу.';

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

/**
 * Folds the list from ConnectionService.Servers() into rows.
 *
 * Go already sanitizes and orders the list (internal/config/servers.go), so
 * this is not a second opinion on it: it covers a call that failed, bindings
 * from another build, and the shaping the picker needs - a readable label and
 * the one hint a row carries. An entry that could not be dialled is dropped
 * rather than drawn as a row that does nothing.
 */
export function savedServerRows(raw: unknown): SavedServerRow[] {
  if (!Array.isArray(raw)) return [];

  const rows: SavedServerRow[] = [];
  const seen = new Set<string>();
  for (const entry of raw) {
    if (typeof entry !== 'object' || entry === null) continue;
    const e = entry as Record<string, unknown>;
    const address = text(e.address);
    const username = text(e.username);
    if (address === '' || username === '') continue;
    if (seen.has(address)) continue;
    seen.add(address);

    const hasPassword = e.hasPassword === true;
    rows.push({
      address,
      username,
      hasPassword,
      label: serverHost(address) || address,
      hint: hasPassword ? PASSWORD_HINT : '',
    });
  }
  return rows;
}

/**
 * What the screen does after ConnectionService.ConnectSaved().
 *
 * - `none`: the connect started; the state events take over from here.
 * - `form`: the stored password could not be read, so the manual form takes
 *   over with the address and nickname already in it. The password is not
 *   lost - the credential store simply would not open - and typing it works.
 * - `refresh`: the row is stale. Re-read the list and say why.
 */
export type ConnectFallback =
  | { kind: 'none' }
  | { kind: 'refresh'; message: string }
  | { kind: 'form'; address: string; username: string; message: string };

/**
 * Reads the result of a click on the picker.
 *
 * It switches on the reason, never on the message: the reason is a value the
 * Go side promises (domain.SavedConnectReason), while the message is a
 * sentence that may be reworded any day. A reason this build does not know
 * refreshes the list and shows whatever the backend said, which is the
 * conservative half of the two - it never sends the user to a form for a
 * failure that had nothing to do with a password.
 */
export function connectFallback(raw: unknown): ConnectFallback {
  if (typeof raw !== 'object' || raw === null) return { kind: 'none' };
  const result = raw as Record<string, unknown>;
  const reason = text(result.reason);
  if (reason === '') return { kind: 'none' };

  const message = text(result.message) || UNKNOWN_FAILURE;
  if (reason !== 'password') return { kind: 'refresh', message };

  return {
    kind: 'form',
    address: text(result.address),
    username: text(result.username),
    message,
  };
}
