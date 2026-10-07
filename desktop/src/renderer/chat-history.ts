import type { ChatEntry } from './media/model.ts';

export const CHAT_HISTORY_LIMIT = 500;
export const CHAT_HISTORY_CHANNEL_LIMIT = 32;

export interface ChatHistoryScope {
  readonly server: string;
  readonly sessionId: number;
}
interface ChannelHistory {
  readonly channelId: number;
  readonly lastId: number;
  readonly entries: readonly ChatEntry[];
}
const EMPTY: readonly ChatEntry[] = Object.freeze([]);
const validID = (id: number, minimum: number) => Number.isSafeInteger(id) && id >= minimum;
const copyEntry = (entry: ChatEntry): ChatEntry =>
  Object.freeze({
    id: entry.id,
    identity: entry.identity,
    name: entry.name,
    text: entry.text,
    local: entry.local,
    time: entry.time,
  });

/** Session transcripts stay in RAM and never cross an authenticated login boundary. */
export class EphemeralChatHistory {
  private scope?: ChatHistoryScope;
  private channels: readonly ChannelHistory[] = Object.freeze([]);

  bind(scope: ChatHistoryScope): void {
    const server = typeof scope.server === 'string' ? scope.server.trim() : '';
    if (!server || !validID(scope.sessionId, 1)) {
      this.reset();
      return;
    }
    if (this.scope?.server === server && this.scope.sessionId === scope.sessionId) return;
    this.reset();
    this.scope = Object.freeze({ server, sessionId: scope.sessionId });
  }

  read(channelId: number): readonly ChatEntry[] {
    if (!this.scope || !validID(channelId, 0)) return EMPTY;
    const channel = this.channels.find((candidate) => candidate.channelId === channelId);
    if (!channel) return EMPTY;
    this.remember(channel);
    return channel.entries;
  }

  /** Merge current controller snapshots. Its IDs increase throughout one login. */
  write(channelId: number, entries: readonly ChatEntry[]): void {
    if (!this.scope || !validID(channelId, 0) || !entries.length) return;
    const previous = this.channels.find((candidate) => candidate.channelId === channelId);
    const lastId = previous?.lastId ?? 0;
    const seen = new Set<number>();
    const fresh = entries
      .filter((entry) => {
        if (!validID(entry.id, 1) || entry.id <= lastId || seen.has(entry.id)) return false;
        seen.add(entry.id);
        return true;
      })
      .map(copyEntry);
    if (!fresh.length) {
      if (previous) this.remember(previous);
      return;
    }
    this.remember(
      Object.freeze({
        channelId,
        lastId: fresh.reduce((highest, entry) => Math.max(highest, entry.id), lastId),
        entries: Object.freeze([...(previous?.entries ?? EMPTY), ...fresh].slice(-CHAT_HISTORY_LIMIT)),
      }),
    );
  }

  reset(): void {
    this.scope = undefined;
    this.channels = Object.freeze([]);
  }

  private remember(channel: ChannelHistory): void {
    this.channels = Object.freeze(
      [...this.channels.filter((candidate) => candidate.channelId !== channel.channelId), channel].slice(
        -CHAT_HISTORY_CHANNEL_LIMIT,
      ),
    );
  }
}
