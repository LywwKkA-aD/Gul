import { useEffect, useRef, useState } from 'react';
import { PaperPlaneRightIcon } from '@phosphor-icons/react/dist/csr/PaperPlaneRight';
import { ChatService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { IconButton, Tooltip } from './ui';

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

  const disabled = channelId === null;

  return (
    // The prototype's composer: one card holding the field and the send
    // button, --s-4 of air to the window edge and to the last message.
    <footer className="shrink-0 px-4 pt-2 pb-4">
      <div
        className={
          'grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-md bg-bg-1 px-2 py-1 ' +
          'shadow-[0_0_0_1px_var(--line),0_1px_2px_rgba(20,27,49,.05)] ' +
          'transition-[box-shadow] duration-[var(--t-fast)] ease-[var(--e-out)] ' +
          'focus-within:shadow-[0_0_0_1px_var(--accent),0_0_0_3px_color-mix(in_oklab,var(--accent)_18%,transparent)]'
        }
      >
        {/* Bare on purpose: the ring belongs to the card around it, so the
            field cannot draw a second edge inside the first. */}
        <input
          ref={inputRef}
          type="text"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && !e.shiftKey && send()}
          placeholder="Написать сообщение…"
          disabled={disabled}
          maxLength={5000}
          className={
            'h-[34px] w-full min-w-0 border-0 bg-transparent px-1 text-text-1 outline-none ' +
            'placeholder:text-text-3 disabled:cursor-default disabled:opacity-50'
          }
        />
        {/* Disabled only while the UI is locked, as in the prototype
            (sendStyle, prototype-source.html:10438): an empty draft is the
            resting state of the composer, and greying the accent out for it
            leaves a washed-out button under the cursor most of the time.
            send() already ignores an empty draft. */}
        <Tooltip label="Отправить — Enter">
          <IconButton surface="accent" aria-label="Отправить" onClick={send} disabled={disabled}>
            <PaperPlaneRightIcon size={15} weight="fill" />
          </IconButton>
        </Tooltip>
      </div>
    </footer>
  );
}
