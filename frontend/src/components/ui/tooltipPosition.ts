// Geometry of the tooltip, kept apart from the component so the rules that
// decide where it lands are testable without a DOM.

/** Distance between the anchor and the tooltip, px. */
export const TOOLTIP_GAP = 6;

/** How close to a window edge the tooltip may come, px. */
export const TOOLTIP_MARGIN = 8;

/** How long the pointer has to rest before the tooltip appears, ms. Long
    enough that crossing a row of buttons does not flash four of them. */
export const TOOLTIP_DELAY_MS = 280;

/** The delay for whole rows - channels, members. The pointer crosses a list
    on its way to one row, and every row it passes must stay quiet. */
export const TOOLTIP_DELAY_ROW_MS = 600;

/** Horizontal centre of the tooltip, folded back into the window.
 *
 * The tooltip is centred on its anchor and drawn with translate(-50%), so the
 * value is a centre and not a left edge. One wider than the window keeps the
 * window's centre: clamping it would only push the text off the other side. */
export function clampTooltipCenter(
  center: number,
  width: number,
  viewport: number,
  margin: number = TOOLTIP_MARGIN,
): number {
  const half = width / 2;
  if (width + margin * 2 >= viewport) return viewport / 2;
  return Math.min(viewport - margin - half, Math.max(margin + half, center));
}

/** Whether the tooltip fits above its anchor. It flips below when it does
    not - a control near the top edge would otherwise point off screen. */
export function fitsAbove(
  anchorTop: number,
  height: number,
  gap: number = TOOLTIP_GAP,
  margin: number = TOOLTIP_MARGIN,
): boolean {
  return anchorTop - gap - height >= margin;
}
