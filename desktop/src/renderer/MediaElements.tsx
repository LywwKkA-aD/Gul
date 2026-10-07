import { useEffect, useRef, useState } from 'react';
import type { Track } from 'livekit-client';

export function Video({ track, local }: { track: Track; local: boolean }) {
  const ref = useRef<HTMLVideoElement>(null);
  const [status, setStatus] = useState('Ожидаем изображение…');
  useEffect(() => {
    const video = ref.current!;
    setStatus('Ожидаем изображение…');
    video.muted = true;
    track.attach(video);
    let frame = 0;
    let last = 0;
    let stopped = false;
    const received = () => {
      if (stopped) return;
      last = performance.now();
      setStatus('');
      frame = video.requestVideoFrameCallback(received);
    };
    frame = video.requestVideoFrameCallback(received);
    const timer = setInterval(() => {
      if (last && performance.now() - last > 5000) setStatus('Изображение приостановлено');
    }, 1000);
    void video.play().catch(() => setStatus('Нажмите на видео, чтобы начать просмотр'));
    return () => {
      stopped = true;
      clearInterval(timer);
      video.cancelVideoFrameCallback(frame);
      track.detach(video);
    };
  }, [track]);
  return (
    <div className="video-wrap">
      <video
        ref={ref}
        autoPlay
        playsInline
        muted
        onClick={() =>
          void ref.current?.play().catch(() => setStatus('Не удалось начать просмотр. Повторите попытку.'))
        }
        aria-label={local ? 'Предпросмотр своего экрана' : 'Демонстрация участника'}
      />
      {status && <span className="video-status">{status}</span>}
    </div>
  );
}

export function Icon({
  name,
  off = false,
}: {
  name:
    | 'mic'
    | 'deaf'
    | 'screen'
    | 'settings'
    | 'leave'
    | 'volumeOff'
    | 'voice'
    | 'send'
    | 'expand'
    | 'collapse'
    | 'close';
  off?: boolean;
}) {
  const paths = {
    close: <path d="M6 6l12 12M18 6L6 18" />,
    mic: (
      <>
        <rect x="9" y="2" width="6" height="12" rx="3" />
        <path d="M5 10v2a7 7 0 0 0 14 0v-2M12 19v3M8 22h8" />
      </>
    ),
    deaf: (
      <>
        <path d="M3 14v-3a9 9 0 0 1 18 0v3" />
        <rect x="3" y="12" width="4" height="9" rx="2" />
        <rect x="17" y="12" width="4" height="9" rx="2" />
      </>
    ),
    screen: (
      <>
        <rect x="2" y="3" width="20" height="14" rx="2" />
        <path d="M8 22h8M12 17v5M12 13V7M9 10l3-3 3 3" />
      </>
    ),
    settings: (
      <>
        <path d="m9 3 1-2h4l1 2 3 2 2 .5 2 3-1 2v3l1 2-2 3-2 .5-3 2-1 2h-4l-1-2-3-2-2-.5-2-3 1-2v-3L2 8.5l2-3 2-.5z" />
        <circle cx="12" cy="12" r="3" />
      </>
    ),
    leave: (
      <>
        <path d="M9 3H3v18h6M14 8l5 4-5 4M8 12h11" />
      </>
    ),
    voice: (
      <>
        <path d="M11 5 6 9H3v6h3l5 4V5M15 8a6 6 0 0 1 0 8M18 5a10 10 0 0 1 0 14" />
      </>
    ),
    volumeOff: (
      <>
        <path d="M11 5 6 9H3v6h3l5 4V5M17 9l5 6M22 9l-5 6" />
      </>
    ),
    send: (
      <>
        <path d="m3 3 18 9-18 9 4-9-4-9ZM7 12h14" />
      </>
    ),
    expand: (
      <>
        <path d="M9 3H3v6M15 3h6v6M21 15v6h-6M9 21H3v-6" />
      </>
    ),
    collapse: (
      <>
        <path d="M3 9h6V3M21 9h-6V3M15 21v-6h6M3 15h6v6" />
      </>
    ),
  };
  return (
    <svg
      viewBox="0 0 24 24"
      width="20"
      height="20"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name]}
      {off && <path className="icon-slash" d="M3 21 21 3" strokeWidth="2" />}
    </svg>
  );
}
