import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { cx } from './cx';

/** primary - the accent button (Connect, "Готово" in Settings).
    quiet   - the accent-outlined button on a light panel ("Проверить микрофон"). */
export type ButtonVariant = 'primary' | 'quiet';

/** The prototype uses two heights: 32px inside the modal, 38px on Connect. */
export type ButtonSize = 'md' | 'lg';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  children: ReactNode;
}

const base =
  'inline-flex items-center justify-center gap-2 rounded-md border-0 font-medium ' +
  'cursor-pointer transition-[background-color,box-shadow] duration-[var(--t-fast)] ' +
  'ease-[var(--e-out)] disabled:cursor-default disabled:opacity-50';

/* Edges are box-shadow rings rather than borders, so the declared height is the
   real height and nothing shifts on focus. */
const variants: Record<ButtonVariant, string> = {
  primary:
    'bg-accent text-on-accent shadow-[0_0_0_1px_var(--accent)] ' +
    'hover:bg-[var(--accent-hover)] hover:shadow-[0_0_0_1px_var(--accent-hover)] ' +
    'active:bg-[var(--accent-active)] active:shadow-[0_0_0_1px_var(--accent-active)]',
  quiet:
    'bg-bg-1 text-[var(--accent-text)] shadow-[0_0_0_1px_var(--accent-line)] ' +
    'hover:bg-[var(--accent-weak)] active:bg-[var(--accent-weak)]',
};

const sizes: Record<ButtonSize, string> = {
  md: 'h-8 px-3 text-sm',
  lg: 'h-[38px] px-4 text-ui',
};

export function Button({
  variant = 'primary',
  size = 'md',
  className,
  children,
  ...rest
}: ButtonProps) {
  return (
    <button type="button" className={cx(base, variants[variant], sizes[size], className)} {...rest}>
      {children}
    </button>
  );
}
