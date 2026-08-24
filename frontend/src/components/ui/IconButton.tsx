import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { cx } from './cx';

/** Which surface the button sits on. The prototype keeps two independent
    colour scales so the light one never leaks into the dark sidebar; `accent`
    is its filled button - the composer's send key. */
export type IconButtonSurface = 'light' | 'sidebar' | 'accent';

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

/* 28x28 is the base control size in the prototype (13 occurrences). The
   background is not set here: every surface below states its own, so two
   background utilities never land on one button and leave the winner to the
   order Tailwind happened to emit them in. */
const base =
  'grid size-7 flex-none place-items-center rounded-md border-0 ' +
  'cursor-pointer transition-[background-color,color] duration-[var(--t-fast)] ' +
  'ease-[var(--e-out)] disabled:cursor-default disabled:opacity-50';

const idle: Record<IconButtonSurface, string> = {
  light: 'bg-transparent text-text-2 hover:bg-bg-3 hover:text-text-1',
  sidebar: 'bg-transparent text-sb-text-2 hover:bg-sb-2 hover:text-sb-text-1',
  accent: 'bg-accent text-on-accent hover:bg-[var(--accent-hover)]',
};

const activeByTone: Record<IconButtonSurface, Record<IconButtonTone, string>> = {
  light: {
    default: 'bg-bg-3 text-text-1',
    danger: 'bg-transparent text-danger hover:bg-bg-3',
  },
  sidebar: {
    // Prototype iconBtn(on): background stays transparent, only the colour moves.
    default: 'bg-sb-2 text-sb-text-1',
    danger: 'bg-transparent text-[var(--sb-danger)] hover:bg-sb-2',
  },
  accent: {
    default: 'bg-[var(--accent-active)] text-on-accent',
    danger: 'bg-danger text-on-accent',
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
