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
import { titlebarHeight } from '../../app/platform';
import { cx } from './cx';
import { TOOLTIP_DELAY_MS, TOOLTIP_GAP, placeTooltip } from './tooltipPosition';

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
  // True from the moment the pointer rests until the chip is shown or given
  // up on. Escape has to reach a tooltip that is still counting down: by the
  // time it is on screen the user has been waiting for it (WAI-ARIA tooltip).
  const [pending, setPending] = useState(false);
  const [place, setPlace] = useState({ left: 0, top: 0 });

  const hide = useCallback(() => {
    clearTimeout(timer.current);
    setPending(false);
    setAnchor(null);
  }, []);

  const show = useCallback(() => {
    clearTimeout(timer.current);
    setPending(true);
    timer.current = setTimeout(() => {
      setPending(false);
      const rect = wrapRef.current?.getBoundingClientRect();
      if (!rect) return;
      setAnchor(rect);
      // A first guess so the chip is never laid out at the window origin; the
      // measured placement lands in the same frame, before the paint.
      setPlace({ left: rect.left, top: rect.top - TOOLTIP_GAP });
    }, delayMs);
  }, [delayMs]);

  useEffect(() => () => clearTimeout(timer.current), []);

  // The rect is measured once, so anything that moves the control out from
  // under the tooltip takes the tooltip down with it.
  useEffect(() => {
    if (!pending && !anchor) return;
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
  }, [pending, anchor, hide]);

  // Corrected before the browser paints: the first position is a guess, and
  // the measured one lands in the same frame. The label is a dependency
  // because the placement owns the centring: a chip that grows while it is
  // shown (the microphone button relabels itself the moment transmission
  // starts) would otherwise keep the left edge computed for the old width.
  useLayoutEffect(() => {
    const tip = tipRef.current;
    if (!anchor || !tip) return;
    const box = tip.getBoundingClientRect();
    setPlace(
      placeTooltip(anchor, box.width, box.height, {
        width: window.innerWidth,
        top: titlebarHeight(),
      }),
    );
  }, [anchor, label]);

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
            // left/top carry the whole placement, including the centring
            // offset, and the entry animation touches opacity only. Two
            // owners of `transform` would fight, and the animation wins:
            // the chip would be drawn on the control for its whole 90ms.
            style={{ left: place.left, top: place.top }}
            className={
              'pointer-events-none fixed z-[var(--z-tooltip)] rounded-sm bg-sb-0 px-2 py-1 ' +
              'text-xs whitespace-nowrap text-sb-text-1 shadow-[var(--sh-md)] ' +
              'animate-[gul-fade-in_var(--t-fast)_var(--e-out)]'
            }
          >
            {label}
          </div>,
          document.body,
        )}
    </span>
  );
}
