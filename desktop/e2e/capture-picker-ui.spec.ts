import { test, expect, _electron as electron } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);

test('synthetic picker UI fits many preview cards and supports keyboard selection, tabs and Escape', async () => {
  // This isolated component proof does not request native capture or connect to any server.
  const directory = await mkdtemp(join(tmpdir(), 'gul-picker-component-'));
  const thumbnail = `data:image/png;base64,${(await readFile(new URL('../../build/appicon.png', import.meta.url))).toString('base64')}`;
  const sources = Array.from({ length: 20 }, (_, index) => ({
    sourceKey: (index + 2).toString(16).padStart(32, '0'),
    kind: index < 2 ? 'screen' : 'window',
    name: index < 2 ? `Экран ${index + 1}` : `Тестовое окно ${index - 1}`,
    thumbnail,
  }));
  await build({
    stdin: {
      contents: `
        import React, {useState} from 'react';
        import {createRoot} from 'react-dom/client';
        import {CapturePickerDialog} from './CapturePicker.tsx';
        import './fonts.css'; import './style.css'; import './capture-picker.css';
        const request=${JSON.stringify({ requestId: '1'.repeat(32), sources, audio: true, details: 'Тестовый предпросмотр: системный захват не запускается.' })};
        function Fixture() {
          const [open,setOpen]=useState(true), [result,setResult]=useState('none');
          return <><button onClick={()=>{setOpen(true);setResult('none');}}>Открыть тестовый выбор</button>
            <output aria-label="Результат выбора">{result}</output>
            {open && <CapturePickerDialog request={request} busy={false} error=""
              onSelect={()=>{setOpen(false);setResult('selected');}}
              onCancel={()=>{setOpen(false);setResult('cancelled');}}/>}</>;
        }
        createRoot(document.getElementById('root')).render(<Fixture/>);
      `,
      resolveDir: fileURLToPath(new URL('../src/renderer', import.meta.url)),
      loader: 'tsx',
    },
    bundle: true,
    platform: 'browser',
    format: 'esm',
    jsx: 'automatic',
    outfile: join(directory, 'ui.js'),
    loader: { '.woff2': 'file' },
    define: { 'process.env.NODE_ENV': '"production"' },
  });
  await writeFile(
    join(directory, 'index.html'),
    `<!doctype html><html lang="ru"><head>
    <meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'none'">
    <link rel="stylesheet" href="ui.css"></head><body><div id="root"></div><script type="module" src="ui.js"></script></body></html>`,
  );
  await writeFile(
    join(directory, 'main.cjs'),
    `
    const {app,BrowserWindow}=require('electron');
    const path=require('node:path');
    app.whenReady().then(()=>{
      const window=new BrowserWindow({width:1000,height:760,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
      window.loadFile(path.join(__dirname,'index.html'));
    });
    app.on('window-all-closed',()=>app.quit());
  `,
  );
  const app = await electron.launch({
    executablePath: require('electron'),
    args: [join(directory, 'main.cjs'), `--user-data-dir=${join(directory, 'profile')}`],
  });
  try {
    const page = await app.firstWindow();
    const dialog = page.getByRole('dialog', { name: 'Демонстрация экрана', exact: true });
    await expect(dialog).toBeVisible();
    const confirm = dialog.getByRole('button', { name: 'Показать', exact: true });
    await expect(confirm).toBeDisabled();
    await dialog.getByRole('tab', { name: /Окна/u }).click();
    await expect(dialog.getByRole('radio')).toHaveCount(18);
    await expect
      .poll(() =>
        dialog
          .locator('.capture-source-preview img')
          .evaluateAll((images) =>
            images.every(
              (image) => image instanceof HTMLImageElement && image.complete && image.naturalWidth === 1024,
            ),
          ),
      )
      .toBe(true);
    const layout = await dialog.evaluate((element) => {
      const grid = element.querySelector('.capture-source-grid')!;
      const area = element.querySelector('#capture-sources')!;
      const cards = [...element.querySelectorAll('.capture-source-card')];
      const buttons = element.querySelector('.capture-picker-actions')!.getBoundingClientRect();
      const visible = element.querySelector('.dialog-scroll')!.getBoundingClientRect();
      return {
        columns: new Set(cards.map((card) => Math.round(card.getBoundingClientRect().left))).size,
        noHorizontalOverflow: grid.scrollWidth <= area.clientWidth,
        scrollable: area.scrollHeight > area.clientHeight,
        controlsInView:
          buttons.bottom <= Math.min(innerHeight, visible.bottom - 20) && buttons.top >= visible.top,
      };
    });
    expect(layout).toEqual({
      columns: 3,
      noHorizontalOverflow: true,
      scrollable: true,
      controlsInView: true,
    });
    await page.screenshot({ path: 'test-results/capture-picker-synthetic-ui.png' });
    const first = dialog.getByRole('radio').nth(0);
    await first.focus();
    await page.keyboard.press('Space');
    await expect(first).toBeChecked();
    await page.keyboard.press('ArrowRight');
    await expect(dialog.getByRole('radio').nth(1)).toBeChecked();
    await expect(confirm).toBeEnabled();
    const last = dialog.locator('.capture-source-card').last();
    await last.scrollIntoViewIfNeeded();
    await last.click();
    await expect(dialog.getByRole('radio').nth(17)).toBeChecked();
    await expect(confirm).toBeVisible();
    await dialog.getByRole('tab', { name: /Экраны/u }).click();
    await expect(confirm).toBeDisabled();
    await dialog.locator('.capture-source-card').first().click();
    await expect(confirm).toBeEnabled();
    await confirm.click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('status', { name: 'Результат выбора' })).toHaveText('selected');
    await page.getByRole('button', { name: 'Открыть тестовый выбор' }).click();
    await expect(dialog).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('status', { name: 'Результат выбора' })).toHaveText('cancelled');
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});
