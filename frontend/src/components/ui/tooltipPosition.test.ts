import test from 'node:test';
import assert from 'node:assert/strict';
import {
  TOOLTIP_GAP,
  TOOLTIP_MARGIN,
  clampTooltipCenter,
  fitsAbove,
  placeTooltip,
} from './tooltipPosition.ts';

// The window is the only container that matters: the tooltip is drawn fixed,
// outside the scrolling panels, so nothing else can clip it.
test('a tooltip with room on both sides keeps its anchor centre', () => {
  assert.equal(clampTooltipCenter(400, 120, 1000), 400);
});

test('a tooltip near an edge is pushed back inside it', () => {
  assert.equal(clampTooltipCenter(10, 120, 1000), TOOLTIP_MARGIN + 60);
  assert.equal(clampTooltipCenter(990, 120, 1000), 1000 - TOOLTIP_MARGIN - 60);
});

// Clamping something wider than the window would only trade one clipped side
// for the other, so it stays centred and clips symmetrically.
test('a tooltip wider than the window keeps the window centre', () => {
  assert.equal(clampTooltipCenter(80, 1200, 1000), 500);
  assert.equal(clampTooltipCenter(80, 1000 - TOOLTIP_MARGIN, 1000), 500);
});

test('a control with room above it keeps the tooltip above', () => {
  assert.equal(fitsAbove(200, 22), true);
  assert.equal(fitsAbove(TOOLTIP_MARGIN + TOOLTIP_GAP + 22, 22), true);
});

test('a control near the top edge flips the tooltip below', () => {
  assert.equal(fitsAbove(20, 22), false);
  assert.equal(fitsAbove(0, 22), false);
});

// macOS keeps the first --titlebar-h pixels for its traffic lights. They are
// drawn over the page, so a chip that "fits" in that band lands on the
// buttons - the band is the top of the usable window, not zero.
test('the titlebar band counts as the top of the window', () => {
  // 57.5 is the sidebar header's own top: it clears y=0 but not a 50px band.
  assert.equal(fitsAbove(57.5, 24), true);
  assert.equal(fitsAbove(57.5, 24, 50), false);
});

test('a control below the titlebar band still keeps its tooltip above', () => {
  assert.equal(fitsAbove(50 + TOOLTIP_MARGIN + TOOLTIP_GAP + 24, 24, 50), true);
});

// placeTooltip returns the chip's own corner: the component sets left/top and
// no transform, so nothing can be overridden by the entry animation.
test('the placement centres the chip on its anchor and lifts it clear', () => {
  const at = placeTooltip(
    { left: 76, top: 720, width: 28, bottom: 748 },
    72,
    24,
    { width: 1100, top: 0 },
  );
  assert.equal(at.left, 76 + 14 - 36);
  assert.equal(at.top, 720 - TOOLTIP_GAP - 24);
});

test('a placement that cannot fit above is dropped below the anchor', () => {
  const at = placeTooltip(
    { left: 12, top: 57.5, width: 200, bottom: 96 },
    120,
    24,
    { width: 1100, top: 50 },
  );
  assert.equal(at.top, 96 + TOOLTIP_GAP);
  assert.ok(at.top > 50, 'the chip must clear the traffic lights');
});

test('a placement near the left edge keeps the chip inside the window', () => {
  const at = placeTooltip({ left: 0, top: 400, width: 20, bottom: 420 }, 120, 24, {
    width: 1100,
    top: 0,
  });
  assert.equal(at.left, TOOLTIP_MARGIN);
});
