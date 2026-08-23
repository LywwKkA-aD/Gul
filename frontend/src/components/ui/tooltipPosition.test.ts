import test from 'node:test';
import assert from 'node:assert/strict';
import {
  TOOLTIP_GAP,
  TOOLTIP_MARGIN,
  clampTooltipCenter,
  fitsAbove,
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
