import type { InputHTMLAttributes } from 'react';
import { cx } from './cx';

export interface TextInputProps extends InputHTMLAttributes<HTMLInputElement> {
  /** Addresses, ports and other machine text use --font-mono at --fs-sm,
      exactly like the "Адрес сервера" field in the prototype. */
  mono?: boolean;
}

/* Prototype inputBase: 34px tall, no border - the 1px edge is a box-shadow
   ring, so focusing cannot shift the layout. The hover/focus rings are the
   prototype's style-hover / style-focus values; the --speak focus outline on
   top of them comes from :focus-visible in base.css. */
const base =
  'h-[34px] w-full min-w-0 rounded-md border-0 bg-bg-1 px-3 text-text-1 ' +
  'shadow-[var(--sh-sm)] transition-[background-color,box-shadow] ' +
  'duration-[var(--t-fast)] ease-[var(--e-out)] ' +
  'placeholder:text-text-3 ' +
  'hover:shadow-[0_0_0_1px_var(--text-3)] ' +
  'focus:shadow-[0_0_0_1px_var(--accent),0_0_0_3px_color-mix(in_oklab,var(--accent)_18%,transparent)] ' +
  'disabled:cursor-default disabled:opacity-50 disabled:hover:shadow-[var(--sh-sm)]';

export function TextInput({ mono = false, className, ...rest }: TextInputProps) {
  return (
    <input
      type="text"
      className={cx(base, mono && 'font-mono text-sm', className)}
      {...rest}
    />
  );
}
