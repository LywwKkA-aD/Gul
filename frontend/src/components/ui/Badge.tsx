import type { ReactNode } from 'react';
import { cx } from './cx';

export interface BadgeProps {
  children: ReactNode;
  className?: string;
}

/** Unread counter next to a channel name: pill on --accent, mono 10px.
    18px tall with a 18px floor on width so single digits stay round. */
export function Badge({ children, className }: BadgeProps) {
  return (
    <span
      className={cx(
        'grid h-[18px] min-w-[18px] flex-none place-items-center rounded-pill',
        'bg-accent px-[5px] font-mono text-[10px] text-on-accent',
        className,
      )}
    >
      {children}
    </span>
  );
}
