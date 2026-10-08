import { useEffect, useRef, useState } from 'react';

/** The thumb follows each input event while asynchronous settings acknowledgements catch up. */
export function SettingsRange({
  value,
  onChange,
  ...input
}: {
  value: number;
  onChange: (value: number) => Promise<void>;
  'aria-label': string;
  min: number;
  max: number;
  step?: number;
  disabled: boolean;
}) {
  const [draft, setDraft] = useState<number | null>(null);
  const revision = useRef(0);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  return (
    <input
      {...input}
      type="range"
      value={draft ?? value}
      onChange={(event) => {
        const next = Number(event.target.value);
        const current = ++revision.current;
        setDraft(next);
        void onChange(next)
          .catch(() => {})
          .finally(() => {
            if (mounted.current && revision.current === current) setDraft(null);
          });
      }}
    />
  );
}
