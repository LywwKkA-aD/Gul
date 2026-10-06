import { useEffect, useRef } from 'react';
import type { ScreenTrack } from './controller';

export function MediaTile({ item }: { item: ScreenTrack }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const audioRef = useRef<HTMLAudioElement>(null);
  useEffect(() => {
    const element = item.kind === 'video' ? videoRef.current : audioRef.current;
    if (!element) return;
    item.track.attach(element);
    return () => { item.track.detach(element); };
  }, [item]);

  if (item.kind === 'audio') {
    // Never attach the sender's own audio, even if the SDK reports it locally.
    return item.local ? null : <audio ref={audioRef} autoPlay data-participant={item.participant} />;
  }
  return (
    <figure className="overflow-hidden rounded-lg bg-sb-0 shadow-[var(--sh-sm)]">
      <video ref={videoRef} autoPlay playsInline muted data-local={String(item.local)} data-participant={item.participant}
        className="aspect-video w-full object-contain" />
      <figcaption className="flex items-center justify-between px-4 py-3 text-sm text-sb-text-1">
        <span>{item.participant}{item.local ? ' · ваш экран' : ''}</span>
        <span className="text-sb-text-2">{item.local ? 'Предпросмотр без звука' : 'Демонстрация'}</span>
      </figcaption>
    </figure>
  );
}
