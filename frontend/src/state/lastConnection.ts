// Remembered connect form input. The password is never part of it: it stays in
// the form for exactly one attempt and is never written anywhere.

/** The official Gul relay, offered by an explicit action - never prefilled. */
export const GUL_RELAY_ADDRESS = 'wss://murmur.gulvox.com/mumble';

export interface LastConnection {
  address: string;
  username: string;
}

const STORAGE_KEY = 'gul.lastConnection';

const EMPTY: LastConnection = { address: '', username: '' };

/** What the last Connect was called with; kept until the server accepts it. */
let attempted: LastConnection | null = null;

/** rememberAttempt records an attempt without storing it yet. */
export function rememberAttempt(next: LastConnection): void {
  attempted = { address: next.address.trim(), username: next.username.trim() };
}

/** commitLastConnection stores the pending attempt. Called when the connection
    is established, so a typo or a dead host is never what comes back. */
export function commitLastConnection(): void {
  if (!attempted || !attempted.address || !attempted.username) return;
  const value = attempted;
  attempted = null;
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
  } catch {
    // A webview with storage disabled costs convenience, not the session.
  }
}

/** loadLastConnection returns the last accepted address and nickname, or empty
    strings when there is nothing usable stored. */
export function loadLastConnection(): LastConnection {
  let raw: string | null = null;
  try {
    raw = localStorage.getItem(STORAGE_KEY);
  } catch {
    return EMPTY;
  }
  if (!raw) return EMPTY;

  try {
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null) return EMPTY;
    const { address, username } = parsed as Partial<LastConnection>;
    return {
      address: typeof address === 'string' ? address : '',
      username: typeof username === 'string' ? username : '',
    };
  } catch {
    return EMPTY;
  }
}
