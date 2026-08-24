// What the window looks like from the outside. The frontend needs exactly one
// platform fact: whether macOS draws its traffic lights over our content.

/** True on macOS, where the window has no native title bar and the traffic
 *  lights float above the top-left corner of the page. */
export const IS_MAC = navigator.userAgent.includes('Mac');

/** Publishes the platform on the root element so CSS can reserve the band
 *  macOS keeps for itself (--titlebar-h in styles/tokens.css). Call once
 *  before the first render; calling it again is harmless. */
export function markPlatform(): void {
  document.documentElement.dataset.platform = IS_MAC ? 'mac' : 'other';
}

/** Publishes whether the window is in fullscreen, where macOS hides the
 *  traffic lights and hands the whole surface back (styles/tokens.css
 *  collapses --titlebar-h to zero). */
export function markFullscreen(fullscreen: boolean): void {
  document.documentElement.dataset.fullscreen = fullscreen ? 'true' : 'false';
}

/** Height of the band the window manager draws over, px.
 *
 * Read from the token rather than from IS_MAC: the same number has to reach
 * layout (CSS) and geometry (the tooltip), and fullscreen moves it. */
export function titlebarHeight(): number {
  const raw = getComputedStyle(document.documentElement).getPropertyValue('--titlebar-h');
  const px = Number.parseFloat(raw);
  return Number.isFinite(px) ? px : 0;
}
