import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import type { CapturePickerDialog } from '../src/renderer/CapturePicker.tsx';
import type { CapturePickerRequest } from '../src/shared/capture-picker.ts';

const compiled = await build({
  stdin: {
    contents: "export { CapturePickerDialog } from './CapturePicker.tsx';",
    resolveDir: fileURLToPath(new URL('../src/renderer', import.meta.url)),
    loader: 'ts',
  },
  bundle: true,
  platform: 'node',
  format: 'cjs',
  packages: 'external',
  jsx: 'automatic',
  write: false,
});
const module = { exports: {} as { CapturePickerDialog: typeof CapturePickerDialog } };
new Function('module', 'exports', 'require', compiled.outputFiles[0].text)(
  module,
  module.exports,
  createRequire(import.meta.url),
);
function render(sources: CapturePickerRequest['sources'], audio = true): string {
  return renderToStaticMarkup(
    createElement(module.exports.CapturePickerDialog, {
      request: { requestId: '1'.repeat(32), sources, audio, details: 'Звук приложений' },
      busy: false,
      error: '',
      onSelect: () => {},
      onCancel: () => {},
    }),
  );
}
const screen = {
  sourceKey: '2'.repeat(32),
  kind: 'screen' as const,
  name: 'Экран 1',
  thumbnail: 'data:image/png;base64,iVBORw0KGgo=',
};
const window = { sourceKey: '3'.repeat(32), kind: 'window' as const, name: 'Игра', thumbnail: null };

test('the app picker presents screen cards with previews and waits for an explicit selection', () => {
  const html = render([screen, window]);
  assert.match(html, /<dialog[^>]+aria-label="Демонстрация экрана"/u);
  assert.match(html, /role="tab"[^>]+aria-selected="true"[^>]*>Экраны/u);
  assert.match(html, /type="radio"[^>]+aria-label="Экран 1"/u);
  assert.match(html, /src="data:image\/png;base64,iVBORw0KGgo="/u);
  assert.match(html, /<button[^>]+disabled=""[^>]*>Показать/u);
  assert.doesNotMatch(html, /type="checkbox"|checked=""/u);
  assert.match(html, /Со звуком компьютера/u);
});

test('window-only capture starts on the window tab and offers a clear preview fallback', () => {
  const html = render([window], false);
  assert.match(html, /role="tab"[^>]+aria-selected="true"[^>]*>Окна/u);
  assert.match(html, /type="radio"[^>]+aria-label="Игра"/u);
  assert.match(html, /Предпросмотр недоступен/u);
  assert.match(html, /Без системного звука/u);
  assert.doesNotMatch(html, /Со звуком компьютера/u);
});

test('native window titles are rendered as text, never markup or image URLs', () => {
  const html = render([{ ...window, name: '<img src=x onerror="alert(1)">' }]);
  assert.match(html, /&lt;img src=x onerror=&quot;alert\(1\)&quot;&gt;/u);
  assert.doesNotMatch(html, /<img src=x|onerror="alert/u);
});
