import { useEffect, useRef, useState } from 'react';
import { PaperPlaneRightIcon } from '@phosphor-icons/react/dist/csr/PaperPlaneRight';
import { ChatService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { IconButton, TextInput, Tooltip } from './ui';

export function Composer({ channelId }: { channelId: number | null }) {
  const [text, setText] = useState('');
  const inputRef = useRef<HTMLInputElement | null>(null);

  // Keep focus on channel switches: typing should never require a click.
  useEffect(() => {
    inputRef.current?.focus();
  }, [channelId]);

  const send = () => {
    const value = text.trim();
    if (!value || channelId === null) return;
    setText('');
    ChatService.Send(channelId, value).catch((err) => {
      console.error(err);
      setText(value); // failed send returns the draft instead of losing it
    });
  };

  return (
    <footer className="shrink-0 px-4 pb-4 pt-1">
      <div className="flex items-center gap-2">
        <TextInput
          ref={inputRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && !e.shiftKey && send()}
          placeholder="Написать сообщение…"
          disabled={channelId === null}
          maxLength={5000}
          className="flex-1"
        />
        <Tooltip label="Отправить — Enter">
          <IconButton
            aria-label="Отправить"
            onClick={send}
            disabled={channelId === null || !text.trim()}
          >
            <PaperPlaneRightIcon size={16} />
          </IconButton>
        </Tooltip>
      </div>
    </footer>
  );
}
