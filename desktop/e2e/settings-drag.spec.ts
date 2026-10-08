import { test, expect, _electron as electron, type Page, type Locator } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
async function drag(page: Page, slider: Locator, fractions: readonly number[]) {
  await slider.scrollIntoViewIfNeeded();
  const bounds = (await slider.boundingBox())!;
  const position = (fraction: number) => bounds.x + 8 + fraction * (bounds.width - 16);
  const value = await slider.inputValue();
  const [min, max] = await slider.evaluate((input: HTMLInputElement) => [
    Number(input.min),
    Number(input.max),
  ]);
  await page.mouse.move(position((Number(value) - min) / (max - min)), bounds.y + bounds.height / 2);
  await page.mouse.down();
  try {
    for (const fraction of fractions) {
      await page.mouse.move(position(fraction), bounds.y + bounds.height / 2, { steps: 4 });
      expect(await slider.isEnabled()).toBe(true);
      const fractionNow = (Number(await slider.inputValue()) - min) / (max - min);
      expect(Math.abs(fractionNow - fraction)).toBeLessThan(0.035);
    }
  } finally {
    await page.mouse.up();
  }
  return Number(await slider.inputValue());
}

test('all settings sliders drag continuously, return to the initial value and persist after reopening', async () => {
  // Real Electron mouse events use the production settings components and save queue.
  // No server, microphone, screen or user profile is accessed by this isolated fixture.
  const directory = await mkdtemp(join(tmpdir(), 'gul-settings-drag-'));
  await build({
    stdin: {
      contents: `
        import React, {useState,useRef} from 'react';
        import {createRoot} from 'react-dom/client';
        import {SettingsDialog} from './SettingsDialog.tsx';
        import {PreferenceUpdateQueue,mergePreferences} from './preference-updates.ts';
        import {RangeUpdates} from './range-updates.ts';
        import {defaultVoiceSettings} from './media/voice-gate.ts';
        import {savePreferences} from './preferences.ts';
        import './fonts.css'; import './style.css'; import './dialogs.css';
        const initial={audioinput:'default',audiooutput:'default',shortcut:'F8',toggleEnabled:false,
          soundNotifications:false,hotkeyMode:'toggle',screenQuality:'720p30',voice:{...defaultVoiceSettings,mode:'vad'}};
        function Fixture(){
          const [open,setOpen]=useState(true),[preferences,setPreferences]=useState(initial);
          const saved=useRef(initial), [queue]=useState(()=>new PreferenceUpdateQueue());
          const [ranges]=useState(()=>new RangeUpdates());
          const [media]=useState(()=>{
            let snapshot={state:'connected',voiceSettings:initial.voice,micLevel:0,voiceProcessingAvailable:true};
            const listeners=new Set();
            return {getSnapshot:()=>snapshot,subscribe:(fn)=>{listeners.add(fn);return()=>listeners.delete(fn);},
              update:(voice)=>{snapshot={...snapshot,voiceSettings:voice};listeners.forEach(fn=>fn());}};
          });
          const change=(patch)=>queue.run(patch,()=>saved.current,async(_next,fields)=>{
            await new Promise(resolve=>setTimeout(resolve,200));
            if(window.__failSettings)throw new Error('Тестовая настройка недоступна.');
            saved.current=mergePreferences(saved.current,fields); media.update(saved.current.voice);
            setPreferences(saved.current); savePreferences(saved.current);
          });
          return <><button onClick={()=>setOpen(true)}>Открыть настройки</button>
            {open&&<SettingsDialog preferences={preferences} media={media} capabilities={null}
              appInfo={null} onChange={change} onAdjust={(patch)=>ranges.run(patch,voice=>change({voice}))}
              onClose={()=>setOpen(false)}/>}</>;
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
    <meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; connect-src 'none'">
    <link rel="stylesheet" href="ui.css"></head><body><div id="root"></div><script type="module" src="ui.js"></script></body></html>`,
  );
  await writeFile(
    join(directory, 'main.cjs'),
    `
    const {app,BrowserWindow,session}=require('electron');
    const path=require('node:path');
    app.whenReady().then(()=>{
      session.defaultSession.setPermissionRequestHandler((_webContents,_permission,respond)=>respond(false));
      const window=new BrowserWindow({width:1000,height:900,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
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
    const dialog = page.getByRole('dialog', { name: 'Настройки', exact: true });
    await expect(dialog).toBeVisible();
    const expected = new Map<string, number>();
    for (const [label, fractions] of [
      ['Усиление микрофона', [0.65, 0.8, 0.95]],
      ['Порог активации', [0.2, 0.45, 0.75]],
      ['Задержка выключения', [0.4, 0.6, 0.85]],
    ] as const) {
      const slider = dialog.getByRole('slider', { name: label, exact: true });
      expected.set(label, await drag(page, slider, fractions));
      expect(expected.get(label)).toBeGreaterThan(label === 'Порог активации' ? -30 : 1);
    }
    const gain = dialog.getByRole('slider', { name: 'Усиление микрофона', exact: true });
    expected.set('Усиление микрофона', await drag(page, gain, [0.6, 0.2, 0.5]));
    expect(expected.get('Усиление микрофона')).toBe(1);
    await dialog.getByRole('button', { name: 'Закрыть: Настройки', exact: true }).click();
    await page.getByRole('button', { name: 'Открыть настройки', exact: true }).click();
    for (const [label, value] of expected) {
      await expect(dialog.getByRole('slider', { name: label, exact: true })).toHaveValue(String(value));
    }
    await expect
      .poll(() => page.evaluate(() => JSON.parse(localStorage.getItem('gul.preferences') ?? '{}').voice))
      .toMatchObject({
        inputGain: 1,
        thresholdDb: expected.get('Порог активации'),
        holdMs: expected.get('Задержка выключения'),
      });
    await drag(page, dialog.getByRole('slider', { name: 'Усиление микрофона', exact: true }), [0.75, 0.9]);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Открыть настройки', exact: true }).click();
    const newer = await drag(
      page,
      dialog.getByRole('slider', { name: 'Усиление микрофона', exact: true }),
      [0.4, 0.2],
    );
    await expect
      .poll(() =>
        page.evaluate(() => JSON.parse(localStorage.getItem('gul.preferences') ?? '{}').voice.inputGain),
      )
      .toBe(newer);
    await expect(dialog.getByRole('slider', { name: 'Усиление микрофона', exact: true })).toHaveValue(
      String(newer),
    );
    await page.evaluate(() => {
      (window as any).__failSettings = true;
    });
    const failedGain = dialog.getByRole('slider', { name: 'Усиление микрофона', exact: true });
    await drag(page, failedGain, [0.6, 0.85]);
    await expect(dialog.getByRole('alert')).toHaveText('Тестовая настройка недоступна.');
    await expect(failedGain).toHaveValue(String(newer));
    await page.evaluate(() => {
      (window as any).__failSettings = false;
    });
    await failedGain.focus();
    await page.keyboard.press('ArrowRight');
    const retried = Math.round((newer + 0.05) * 100) / 100;
    await expect(failedGain).toHaveValue(String(retried));
    await expect
      .poll(() =>
        page.evaluate(() => JSON.parse(localStorage.getItem('gul.preferences') ?? '{}').voice.inputGain),
      )
      .toBe(retried);
    await expect(dialog.getByRole('alert')).toHaveCount(0);
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});
