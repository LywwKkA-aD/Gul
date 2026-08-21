import type { ReactNode } from 'react';
import { cx } from './cx';

export interface FieldProps {
  /** Caption above the control: uppercase, --fs-xs, letter-spacing .08em. */
  label: ReactNode;
  children: ReactNode;
  className?: string;
  htmlFor?: string;
}

/** Label + control pair from the prototype's Connect card and Settings tabs.
    Renders as a <label>, so clicking the caption focuses the control even when
    no htmlFor is given. */
export function Field({ label, children, className, htmlFor }: FieldProps) {
  return (
    <label htmlFor={htmlFor} className={cx('flex min-w-0 flex-col gap-2', className)}>
      <span className="text-xs tracking-[.08em] text-text-3 uppercase">{label}</span>
      {children}
    </label>
  );
}
