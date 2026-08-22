// Two class strings that several settings controls share. They are prototype
// styles rather than components: the markup around them differs every time
// (a Field label, a caption with a value readout, a bare aria-label).

/** Prototype selectStyle: the input base with the native chrome stripped -
    34px tall, no border, the 1px edge is a box-shadow ring. */
export const selectClass =
  'h-[34px] w-full min-w-0 cursor-pointer appearance-none rounded-md border-0 bg-bg-1 px-3 ' +
  'text-text-1 shadow-[var(--sh-sm)] transition-[background-color,box-shadow] ' +
  'duration-[var(--t-fast)] ease-[var(--e-out)] hover:shadow-[0_0_0_1px_var(--text-3)] ' +
  'focus:shadow-[0_0_0_1px_var(--accent),0_0_0_3px_color-mix(in_oklab,var(--accent)_18%,transparent)]';

/** Caption above a control: uppercase, --fs-xs, letter-spacing .08em. */
export const captionClass = 'text-xs tracking-[.08em] text-text-3 uppercase';
