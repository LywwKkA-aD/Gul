import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { cx } from './cx';

/** Which surface the button sits on. The prototype keeps two independent
    colour scales so the light one never leaks into the dark sidebar. */
export type IconButtonSurface = 'light' | 'sidebar';

/** `danger` only changes how the *active* state is painted - that is exactly
    how the prototype uses it for the mic-mute / deafen toggles. */
export type IconButtonTone = 'default' | 'danger';

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  surface?: IconButtonSurface;
  tone?: IconButtonTone;
  /** Leave undefined for plain buttons; set it to make the button a toggle,
      which also emits aria-pressed. */
  active?: boolean;
  children: ReactNode;
}

/* 28x28 is the base control size in the prototype (13 occurrences). */
const base =
  'grid size-7 flex-none place-items-center rounded-md border-0 bg-transparent ' +
  'cursor-pointer transition-[background-color,color] duration-[var(--t-fast)] ' +
  'ease-[var(--e-out)] disabled:cursor-default disabled:opacity-50';

const idle: Record<IconButtonSurface, string> = {
  light: 'text-text-2 hover:bg-bg-3 hover:text-text-1',
  sidebar: 'text-sb-text-2 hover:bg-sb-2 hover:text-sb-text-1',
};

const activeByTone: Record<IconButtonSurface, Record<IconButtonTone, string>> = {
  light: {
    default: 'bg-bg-3 text-text-1',
    danger: 'text-danger hover:bg-bg-3',
  },
  sidebar: {
    // Prototype iconBtn(on): background stays transparent, only the colour moves.
    default: 'bg-sb-2 text-sb-text-1',
    danger: 'text-[var(--sb-danger)] hover:bg-sb-2',
  },
};

export function IconButton({
  surface = 'light',
  tone = 'default',
  active,
  className,
  children,
  ...rest
}: IconButtonProps) {
  return (
    <button
      type="button"
      aria-pressed={active}
      className={cx(base, active ? activeByTone[surface][tone] : idle[surface], className)}
      {...rest}
    >
      {children}
    </button>
  );
}
