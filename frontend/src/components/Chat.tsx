import { useEffect, useMemo, useRef } from 'react';
import { Browser } from '@wailsio/runtime';
import { HashIcon } from '@phosphor-icons/react/dist/csr/Hash';
import { ChatCircleDotsIcon } from '@phosphor-icons/react/dist/csr/ChatCircleDots';
import { useGulStore } from '../state/store';
import type { ChannelNode, ChatMessage } from '../state/types';
import { initialsOf, tintFor } from '../state/types';
import { Avatar } from './ui';
import { cx } from './ui/cx';
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
      {/* Starts at the top edge of the window: on macOS the first
          --titlebar-h pixels are a window drag handle, and a channel name is a
          label, not a control - so it may live there, as long as the header
          covers the whole band (--top-header-h in styles/tokens.css). Off
          macOS the band is zero and this is a plain 46px header that carries
          the drag region itself. */}
      <header
        className="flex h-[var(--top-header-h)] shrink-0 items-center gap-2 pr-3 pl-4 shadow-[0_1px_0_var(--line)]"
        style={{ '--wails-draggable': 'drag' } as React.CSSProperties}
      >
        <HashIcon size={15} className="flex-none text-text-3" />
        <h2 className="min-w-0 truncate font-medium">{channel?.name ?? '…'}</h2>
      </header>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        onClickCapture={onClickCapture}
        className="flex min-h-0 flex-1 flex-col overflow-y-auto"
      >
        {/* Short conversations sit on the composer instead of floating at the
            top of an empty canvas - the prototype's `margin-top:auto`. */}
        <div className="mt-auto pt-4 pb-3">
          {rows.length === 0 ? (
            <EmptyChannel />
          ) : (
            rows.map((row) => <MessageRow key={row.message.id} {...row} />)
          )}
        </div>
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
    // The prototype's message grid: a 36px gutter for the avatar, --s-3 to the
    // text, --s-4 to the window edge. A follow-up message keeps the gutter, so
    // every line of a group starts on the same left edge.
    <div
      className={cx(
        'grid grid-cols-[36px_minmax(0,1fr)] gap-3 px-4',
        'transition-colors duration-[var(--t-fast)] hover:bg-bg-0',
        head && 'mt-2 first:mt-0',
      )}
      style={{ paddingTop: 'var(--row-pad-y)', paddingBottom: 'var(--row-pad-y)' }}
    >
      <div className="col-start-1">
        {head && (
          <Avatar
            size={36}
            tint={tintFor({ hash: message.senderHash, name: message.sender })}
            initials={initialsOf(message.sender)}
            className="mt-0.5"
          />
        )}
      </div>
      <div className="col-start-2 min-w-0">
        {head && (
          <p className="mb-0.5 flex items-baseline gap-2">
            <span className="min-w-0 truncate font-medium">{message.sender}</span>
            <span className="flex-none font-mono text-xs text-text-3">{time}</span>
          </p>
        )}
        <div
          className="chat-html text-text-1 [overflow-wrap:anywhere] [text-wrap:pretty]"
          // Safe by contract: sanitized in Go (internal/core/sanitize.go),
          // only b/i/u/br and http(s) links survive.
          dangerouslySetInnerHTML={{ __html: message.html }}
        />
      </div>
    </div>
  );
}

/* Left aligned and sitting on the composer, exactly where the first message
   will appear - a centred block would move the eye somewhere the conversation
   never starts. */
function EmptyChannel() {
  return (
    <div className="flex flex-col items-start gap-2 px-4 py-6">
      <div className="grid size-[34px] place-items-center rounded-md bg-bg-3 shadow-[var(--sh-sm)]">
        <ChatCircleDotsIcon size={18} className="text-text-3" />
      </div>
      <p className="font-medium">Здесь пока тихо</p>
      <p className="max-w-[44ch] text-sm text-text-2">
        Напишите первым — остальные подтянутся.
      </p>
    </div>
  );
}
