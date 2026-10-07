import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { ChatEntry } from './media/model.ts';
import { Icon } from './MediaElements.tsx';

export function ChatPanel({
  entries,
  connected,
  onSend,
  onError,
}: {
  entries: readonly ChatEntry[];
  connected: boolean;
  onSend: (text: string) => Promise<void>;
  onError: (message: string) => void;
}) {
  const [message, setMessage] = useState('');
  const [sending, setSending] = useState(false);
  const [unread, setUnread] = useState(false);
  const scroll = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const nearBottom = useRef(true);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    input.current?.focus();
    return () => {
      mounted.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    const container = scroll.current;
    if (!container) return;
    if (nearBottom.current) container.scrollTop = container.scrollHeight;
    else setUnread(true);
  }, [entries]);
  useEffect(() => {
    const container = scroll.current;
    if (!container) return;
    const observer = new ResizeObserver(() => {
      if (nearBottom.current) container.scrollTop = container.scrollHeight;
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, []);
  const jumpToBottom = () => {
    nearBottom.current = true;
    setUnread(false);
    const container = scroll.current;
    if (container) container.scrollTop = container.scrollHeight;
  };
  const send = async () => {
    const text = message.trim();
    if (!text || sending || !connected) return;
    setSending(true);
    try {
      await onSend(text);
      if (!mounted.current) return;
      setMessage('');
      jumpToBottom();
    } catch (failure) {
      if (mounted.current)
        onError(failure instanceof Error ? failure.message : 'Не удалось отправить сообщение.');
    } finally {
      if (mounted.current) {
        setSending(false);
        input.current?.focus();
      }
    }
  };
  return (
    <>
      <div
        ref={scroll}
        className="chat"
        aria-label="Чат"
        onScroll={() => {
          const container = scroll.current!;
          nearBottom.current = container.scrollHeight - container.scrollTop - container.clientHeight < 80;
          if (nearBottom.current) setUnread(false);
        }}
      >
        <div className="chat-welcome">
          <h2>Здесь начинается разговор</h2>
          <p className="subtle">Вы в канале. Можно говорить или написать.</p>
        </div>
        {entries.map((chat) => (
          <article className="chat-message" key={chat.id}>
            <div className="avatar small">{chat.name.slice(0, 1).toUpperCase()}</div>
            <div>
              <div className="message-heading">
                <strong>{chat.name}</strong>
                <time dateTime={new Date(chat.time).toISOString()}>
                  {new Date(chat.time).toLocaleTimeString('ru', { hour: '2-digit', minute: '2-digit' })}
                </time>
              </div>
              <p>{chat.text}</p>
            </div>
          </article>
        ))}
      </div>
      {unread && (
        <button className="new-messages" onClick={jumpToBottom}>
          Перейти к новым сообщениям
        </button>
      )}
      <form
        className="chat-compose"
        onSubmit={(event) => {
          event.preventDefault();
          void send();
        }}
      >
        <input
          ref={input}
          aria-label="Сообщение"
          placeholder="Написать в канал…"
          value={message}
          onChange={(event) => setMessage(event.target.value)}
          maxLength={5000}
          disabled={!connected || sending}
        />
        <button aria-label="Отправить сообщение" disabled={!connected || sending || !message.trim()}>
          <Icon name="send" />
        </button>
      </form>
    </>
  );
}
