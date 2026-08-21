import { useEffect, useMemo, useRef } from 'react';
import { Browser } from '@wailsio/runtime';
import { HashIcon } from '@phosphor-icons/react/dist/csr/Hash';
import { ChatCircleDotsIcon } from '@phosphor-icons/react/dist/csr/ChatCircleDots';
import { useGulStore } from '../state/store';
import type { ChannelNode, ChatMessage } from '../state/types';
import { initialsOf, tintFor } from '../state/types';
import { Avatar } from './ui';
import { Composer } from './Composer';

// Consecutive messages of one sender within this window collapse into a group.
const GROUP_WINDOW_MS = 5 * 60 * 1000;

// Stable empty history: a zustand selector must return the same reference for
// the same state, otherwise useSyncExternalStore loops forever (white screen).
const NO_MESSAGES: ChatMessage[] = [];

export function Chat({ channel }: { channel: ChannelNode | null }) {
  const messages = useGulStore((s) => (channel ? (s.messages[channel.id] ?? NO_MESSAGES) : NO_MESSAGES));
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const pinnedToBottom = useRef(true);

  // Autoscroll only while the user is already at the bottom.
  useEffect(() => {
    const el = scrollRef.current;
    if (el && pinnedToBottom.current) el.scrollTop = el.scrollHeight;
  }, [messages]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    pinnedToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  };

  // Links in sanitized HTML open in the system browser, never in the webview.
  const onClickCapture = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest('a');
    if (a?.href) {
      e.preventDefault();
      Browser.OpenURL(a.href).catch(console.error);
    }
  };

  const rows = useMemo(() => groupRows(messages), [messages]);

  return (
    <>
      <header
        className="flex h-12 shrink-0 items-center gap-2 px-4 shadow-[0_1px_0_var(--line)]"
        style={{ '--wails-draggable': 'drag' } as React.CSSProperties}
      >
        <HashIcon size={16} className="text-text-3" />
        <h2 className="min-w-0 truncate text-sm font-medium">{channel?.name ?? '…'}</h2>
      </header>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        onClickCapture={onClickCapture}
        className="min-h-0 flex-1 overflow-y-auto px-4 py-3"
      >
        {rows.length === 0 ? (
          <EmptyChannel />
        ) : (
          rows.map((row) => <MessageRow key={row.message.id} {...row} />)
        )}
      </div>

      <Composer channelId={channel?.id ?? null} />
    </>
  );
}

interface Row {
  message: ChatMessage;
  head: boolean;
}

function groupRows(messages: ChatMessage[]): Row[] {
  return messages.map((m, i) => {
    const prev = messages[i - 1];
    const head =
      !prev ||
      prev.sender !== m.sender ||
      new Date(m.at).getTime() - new Date(prev.at).getTime() > GROUP_WINDOW_MS;
    return { message: m, head };
  });
}

function MessageRow({ message, head }: Row) {
  const time = new Date(message.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

  return (
    <div
      className={head ? 'mt-3 flex gap-3' : 'flex gap-3'}
      style={{ paddingTop: 'var(--row-pad-y)', paddingBottom: 'var(--row-pad-y)' }}
    >
      <div className="w-9 shrink-0">
        {head && (
          <Avatar
            size={36}
            tint={tintFor({ hash: message.senderHash, name: message.sender })}
            initials={initialsOf(message.sender)}
          />
        )}
      </div>
      <div className="min-w-0 flex-1">
        {head && (
          <p className="mb-0.5 flex items-baseline gap-2">
            <span className="text-sm font-medium">{message.sender}</span>
            <span className="text-xs text-text-3">{time}</span>
          </p>
        )}
        <div
          className="chat-html break-words text-sm leading-relaxed text-text-2"
          // Safe by contract: sanitized in Go (internal/core/sanitize.go),
          // only b/i/u/br and http(s) links survive.
          dangerouslySetInnerHTML={{ __html: message.html }}
        />
      </div>
    </div>
  );
}

function EmptyChannel() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-text-3">
      <ChatCircleDotsIcon size={40} weight="light" />
      <p className="text-sm">Здесь пока тихо. Напишите первым.</p>
    </div>
  );
}
