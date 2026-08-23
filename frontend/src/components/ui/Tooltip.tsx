import {
  cloneElement,
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type FocusEvent,
  type ReactElement,
} from 'react';
import { createPortal } from 'react-dom';
import { cx } from './cx';
import {
  TOOLTIP_DELAY_MS,
  TOOLTIP_GAP,
  clampTooltipCenter,
  fitsAbove,
} from './tooltipPosition';

/** The prototype's tooltip (docs/design/prototype-source.html, tipStyle): a
 *  dark chip above the control, drawn fixed and portalled to the body so the
 *  scrolling panels cannot clip it.
 *
 *  For controls, not for prose: a truncated name keeps the native `title`,
 *  which is what the browser already does well. Every control wrapped here
 *  keeps its own accessible name (aria-label); the tooltip only describes it.
 *
 *  Not for controls inside a modal: the token scale puts --z-tooltip (200)
 *  below --z-modal (300), so a tooltip raised from a dialog would be drawn
 *  under it. Those keep a native title. */

/** A control that can carry aria-describedby - that is how the label reaches
    assistive technology instead of being a picture of text. */
type Describable = ReactElement<{ 'aria-describedby'?: string }>;

export interface TooltipProps {
  /** One short Russian line. */
  label: string;
  children: Describable;
  /** Utility classes for the wrapper; it is inline-flex by default and has to
      be told when the control below it fills its row. */
  className?: string;
  delayMs?: number;
}

export function Tooltip({ label, children, className, delayMs = TOOLTIP_DELAY_MS }: TooltipProps) {
  const id = useId();
  const wrapRef = useRef<HTMLSpanElement>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // The rect the tooltip is anchored to; null means it is not shown.
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const [place, setPlace] = useState({ left: 0, below: false });

  const hide = useCallback(() => {
    clearTimeout(timer.current);
    setAnchor(null);
  }, []);

  const show = useCallback(() => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      const rect = wrapRef.current?.getBoundingClientRect();
      if (!rect) return;
      setAnchor(rect);
      setPlace({ left: rect.left + rect.width / 2, below: false });
    }, delayMs);
  }, [delayMs]);

  useEffect(() => () => clearTimeout(timer.current), []);

  // The rect is measured once, so anything that moves the control out from
  // under the tooltip takes the tooltip down with it.
  useEffect(() => {
    if (!anchor) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') hide();
    };
    window.addEventListener('scroll', hide, true);
    window.addEventListener('resize', hide);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('scroll', hide, true);
      window.removeEventListener('resize', hide);
      window.removeEventListener('keydown', onKey);
    };
  }, [anchor, hide]);

  // Corrected before the browser paints: the first position is the plain
  // anchor centre, and the measured one lands in the same frame.
  useLayoutEffect(() => {
    const tip = tipRef.current;
    if (!anchor || !tip) return;
    setPlace({
      left: clampTooltipCenter(anchor.left + anchor.width / 2, tip.offsetWidth, window.innerWidth),
      below: !fitsAbove(anchor.top, tip.offsetHeight),
    });
  }, [anchor]);

  const onFocus = (e: FocusEvent<HTMLSpanElement>) => {
    // A tooltip on a control the user has just clicked is noise, and
    // :focus-visible is exactly the "reached by keyboard" distinction the
    // browser already makes.
    if (e.target instanceof HTMLElement && !e.target.matches(':focus-visible')) return;
    show();
  };

  return (
    <span
      ref={wrapRef}
      className={cx('inline-flex', className)}
      onPointerEnter={show}
      onPointerLeave={hide}
      onPointerDown={hide}
      onFocus={onFocus}
      onBlur={hide}
    >
      {cloneElement(children, { 'aria-describedby': anchor ? id : undefined })}
      {anchor !== null &&
        createPortal(
          <div
            ref={tipRef}
            id={id}
            role="tooltip"
            style={{
              left: place.left,
              top: place.below ? anchor.bottom + TOOLTIP_GAP : anchor.top - TOOLTIP_GAP,
              transform: place.below ? 'translate(-50%,0)' : 'translate(-50%,-100%)',
            }}
            className={
              'pointer-events-none fixed z-[var(--z-tooltip)] rounded-sm bg-sb-0 px-2 py-1 ' +
              'text-xs whitespace-nowrap text-sb-text-1 shadow-[var(--sh-md)] ' +
              'animate-[gul-in_var(--t-fast)_var(--e-out)]'
            }
          >
            {label}
          </div>,
          document.body,
        )}
    </span>
  );
}
