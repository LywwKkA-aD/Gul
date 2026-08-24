import { useEffect, type CSSProperties } from 'react';
import { ChatService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { findChannel, selfUser, useGulStore } from '../state/store';
import type { ChatMessage, ConnState } from '../state/types';
import { ChannelTree } from '../components/ChannelTree';
import { Chat } from '../components/Chat';
import { MemberList } from '../components/MemberList';
import { BottomBar } from '../components/BottomBar';
import { ReconnectBanner } from '../components/ReconnectBanner';
import { Tooltip } from '../components/ui';
import { cx } from '../components/ui/cx';
import { IS_MAC } from './platform';
import { useFullscreen } from './fullscreen';
import { serverLabel } from './serverLabel';

// Wails: elements with this CSS property act as the window drag handle. On
// macOS the top --titlebar-h of the window is a native drag area already
// (main.go, InvisibleTitleBarHeight), so this is what carries Windows and
// Linux, where the headers are the only thing to grab.
const DRAG_REGION = { '--wails-draggable': 'drag' } as CSSProperties;

const STATE_LABEL: Record<ConnState, string> = {
  connected: 'в сети',
  connecting: 'подключение…',
  reconnecting: 'переподключение…',
  disconnected: 'нет связи',
};

const STATE_TONE: Record<ConnState, string> = {
  connected: 'text-sb-text-3',
  connecting: 'text-sb-text-3',
  reconnecting: 'text-warning',
  disconnected: 'text-[var(--sb-danger)]',
};

const COLUMNS = 'grid-cols-[var(--sidebar-w)_minmax(0,1fr)_var(--members-w)]';

export function MainScreen() {
  const status = useGulStore((s) => s.status);
  const tree = useGulStore((s) => s.tree);
  const activeChannelId = useGulStore((s) => s.activeChannelId);
  const setActiveChannel = useGulStore((s) => s.setActiveChannel);
  const setHistory = useGulStore((s) => s.setHistory);
  // macOS hides its traffic lights in fullscreen, so the band we keep clear
  // for them stops being anything but dead pixels (app/fullscreen.ts).
  const fullscreen = useFullscreen();

  const reconnecting = status.state === 'reconnecting';
  const self = selfUser(tree);

  // Follow our own position in the tree: joins (ours or forced by an admin)
  // move the active channel.
  useEffect(() => {
    if (self && self.channelId !== activeChannelId) {
      setActiveChannel(self.channelId);
    }
  }, [self, activeChannelId, setActiveChannel]);

  // Refill history on channel switch (covers frontend hot reload too).
  useEffect(() => {
    if (activeChannelId === null) return;
    ChatService.History(activeChannelId)
      .then((msgs) => setHistory(activeChannelId, (msgs ?? []) as unknown as ChatMessage[]))
      .catch(console.error);
  }, [activeChannelId, setHistory]);

  const activeChannel = activeChannelId !== null ? findChannel(tree, activeChannelId) : null;
  const label = serverLabel(status.server, tree?.name) || 'СЕРВЕР';
  const address = status.server || 'Сервер не выбран';

  return (
    <div className="fixed inset-0 flex flex-col bg-bg-0 text-ui text-text-1">
      {IS_MAC && !fullscreen && <TitleBarStrip />}

      <div
        className={cx(
          'grid min-h-0 flex-1',
          COLUMNS,
          reconnecting && 'pointer-events-none opacity-55 select-none',
        )}
      >
        <aside
          data-sidebar
          className="flex min-h-0 flex-col bg-sb-1 text-sb-text-1 shadow-[1px_0_0_var(--sb-line)]"
        >
          <header
            className="flex h-[var(--header-h)] shrink-0 items-center px-3 shadow-[0_1px_0_var(--sb-line)]"
            style={DRAG_REGION}
          >
            {/* The full address lives in the tooltip rather than in a native
                title: two tips on one element would fire one after the other.
                The row is a drag handle, so it cannot become a button - but it
                is a tab stop, which is what the tooltip's focus path needs and
                what gives a keyboard the address the truncation hides. The
                visible text is the picture of it; the accessible sentence
                below carries the whole thing. */}
            <Tooltip label={address} className="min-w-0 flex-1">
              {/* A bare focusable span has role generic, which takes no name
                  from its contents, so the sentence is also the label. */}
              <span
                tabIndex={0}
                aria-label={`${address}. Состояние: ${STATE_LABEL[status.state]}`}
                className="flex min-w-0 flex-col rounded-sm leading-tight"
              >
                <span
                  aria-hidden="true"
                  className="truncate font-display text-[12px] font-medium tracking-[.04em] text-sb-text-1"
                >
                  {label}
                </span>
                <span
                  aria-hidden="true"
                  className={cx('truncate text-xs', STATE_TONE[status.state])}
                >
                  {STATE_LABEL[status.state]}
                </span>
              </span>
            </Tooltip>
          </header>

          <div className="min-h-0 flex-1 overflow-y-auto px-2 pt-3 pb-4">
            <ChannelTree />
          </div>

          <BottomBar />
        </aside>

        <section className="flex min-h-0 min-w-0 flex-col bg-bg-2">
          <Chat channel={activeChannel} />
        </section>

        <MemberList channel={activeChannel} />
      </div>

      {reconnecting && <ReconnectBanner server={status.server} note={status.error} />}
    </div>
  );
}

/** The band macOS keeps for its traffic lights. It stays empty on purpose:
 *  Wails turns those pixels into a window drag handle, so a control placed
 *  there would start dragging the window instead of reacting. Each cell keeps
 *  its column's surface, so the strip reads as an inset and not as a bar. */
function TitleBarStrip() {
  return (
    <div className={cx('grid h-[var(--titlebar-h)] shrink-0', COLUMNS)} style={DRAG_REGION}>
      <div className="bg-sb-1 shadow-[1px_0_0_var(--sb-line)]" />
      <div className="bg-bg-2" />
      <div className="bg-bg-1 shadow-[-1px_0_0_var(--line)]" />
    </div>
  );
}
