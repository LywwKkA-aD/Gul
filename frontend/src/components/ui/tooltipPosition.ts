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

/** The part of an anchor's rect the placement needs. */
export interface AnchorBox {
  left: number;
  top: number;
  width: number;
  bottom: number;
}

/** The window as the tooltip may use it.
 *
 * `top` is not always zero: on macOS the window has no title bar of its own
 * and the traffic lights float over the first --titlebar-h pixels of the page
 * (styles/tokens.css), which are neither ours to draw on nor clickable. */
export interface TooltipViewport {
  width: number;
  top: number;
}

/** Where the tooltip's own top-left corner goes, in viewport coordinates. */
export interface TooltipPlacement {
  left: number;
  top: number;
}

/** Horizontal centre of the tooltip, folded back into the window.
 *
 * The value is a centre and not a left edge: the chip is centred on its
 * anchor. One wider than the window keeps the window's centre - clamping it
 * would only push the text off the other side. */
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
 *  not - a control near the top edge would otherwise point off screen, and on
 *  macOS it would land on the traffic lights, which draw over everything. */
export function fitsAbove(
  anchorTop: number,
  height: number,
  viewportTop: number = 0,
  gap: number = TOOLTIP_GAP,
  margin: number = TOOLTIP_MARGIN,
): boolean {
  return anchorTop - gap - height >= viewportTop + margin;
}

/** The tooltip's own top-left corner.
 *
 * The offsets are folded in here rather than left to a CSS transform: the
 * entry animation is drawn by the same engine, and an animated transform beats
 * an inline one - the chip would be laid out uncentred for the whole of it. */
export function placeTooltip(
  anchor: AnchorBox,
  width: number,
  height: number,
  viewport: TooltipViewport,
): TooltipPlacement {
  const center = clampTooltipCenter(anchor.left + anchor.width / 2, width, viewport.width);
  const above = fitsAbove(anchor.top, height, viewport.top);
  return {
    left: center - width / 2,
    top: above ? anchor.top - TOOLTIP_GAP - height : anchor.bottom + TOOLTIP_GAP,
  };
}
