import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// --titlebar-h is a hand-copy of the window option Go passes to Wails. Nothing
// links the two at build time, and the cost of drift is silent: the strip that
// keeps macOS its traffic-light band would be the wrong height, and every
// click in the difference would drag the window instead of reaching a control.
// So the copy is checked here, where it is cheap to notice.

function read(relative: string): string {
  return readFileSync(fileURLToPath(new URL(relative, import.meta.url)), 'utf8');
}

function only(source: string, pattern: RegExp, what: string): number {
  const hits = [...source.matchAll(pattern)];
  assert.equal(hits.length, 1, `expected exactly one ${what}, found ${hits.length}`);
  return Number(hits[0][1]);
}

test('--titlebar-h matches main.go InvisibleTitleBarHeight', () => {
  const go = only(read('../../../main.go'), /InvisibleTitleBarHeight:\s*(\d+)/g, 'window option');

  const tokens = read('../styles/tokens.css');
  const mac = tokens.match(/:root\[data-platform="mac"\]\s*\{([^}]*)\}/);
  assert.ok(mac, 'tokens.css has no :root[data-platform="mac"] block');
  const css = only(mac[1], /--titlebar-h:\s*(\d+)px/g, 'titlebar token');

  assert.equal(
    css,
    go,
    `--titlebar-h is ${css}px but main.go passes InvisibleTitleBarHeight: ${go}`,
  );
});

// Everywhere else the band does not exist, and a non-zero default would push
// the whole window down on Windows and Linux.
test('the titlebar band is zero by default', () => {
  const tokens = read('../styles/tokens.css');
  const root = tokens.match(/:root\s*\{([\s\S]*?)\n\}/);
  assert.ok(root, 'tokens.css has no bare :root block');
  assert.equal(only(root[1], /--titlebar-h:\s*(\d+)px/g, 'titlebar token'), 0);
});
