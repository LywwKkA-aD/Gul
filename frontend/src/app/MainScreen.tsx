import { useEffect, type CSSProperties } from 'react';
import { ChatService } from '../../bindings/gul/services';
import { findChannel, selfUser, useGulStore } from '../state/store';
import type { ChatMessage } from '../state/types';
import { ChannelTree } from '../components/ChannelTree';
import { Chat } from '../components/Chat';
import { MemberList } from '../components/MemberList';
import { BottomBar } from '../components/BottomBar';
import { ReconnectBanner } from '../components/ReconnectBanner';

const IS_MAC = navigator.userAgent.includes('Mac');
// Wails: elements with this CSS property act as the window drag handle.
const DRAG_REGION = { '--wails-draggable': 'drag' } as CSSProperties;

export function MainScreen() {
  const status = useGulStore((s) => s.status);
  const tree = useGulStore((s) => s.tree);
  const activeChannelId = useGulStore((s) => s.activeChannelId);
  const setActiveChannel = useGulStore((s) => s.setActiveChannel);
  const setHistory = useGulStore((s) => s.setHistory);

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

  return (
    <div className="fixed inset-0 grid grid-rows-[minmax(0,1fr)] bg-bg-0 text-ui text-text-1">
      <div
        className={
          'grid min-h-0 grid-cols-[240px_minmax(0,1fr)_auto] ' +
          (reconnecting ? 'pointer-events-none opacity-55 select-none' : '')
        }
      >
        <aside data-sidebar className="flex min-h-0 flex-col bg-sb-1 text-sb-text-1">
          <header
            className={
              'flex h-12 shrink-0 items-center shadow-[0_1px_0_var(--sb-line)] ' +
              // Hidden-inset titlebar on macOS: keep clear of the traffic lights.
              (IS_MAC ? 'pl-[78px] pr-4' : 'px-4')
            }
            style={DRAG_REGION}
          >
            <span className="min-w-0 truncate font-display text-[13px] tracking-wide">
              {status.server || 'СЕРВЕР'}
            </span>
          </header>
          <div className="min-h-0 flex-1 overflow-y-auto py-2">
            <ChannelTree />
          </div>
          <BottomBar />
        </aside>

        <section className="flex min-h-0 min-w-0 flex-col bg-bg-2">
          <Chat channel={activeChannel} />
        </section>

        <MemberList channel={activeChannel} />
      </div>

      {reconnecting && <ReconnectBanner server={status.server} />}
    </div>
  );
}
