import { test, expect, _electron as electron } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);

test('screen presets require confirmation, persist and apply to the next share without restarting an active one', async () => {
  // The production App and dialogs run with a fake media controller. No native capture or network is available.
  const directory = await mkdtemp(join(tmpdir(), 'gul-screen-quality-'));
  const renderer = fileURLToPath(new URL('../src/renderer', import.meta.url));
  await build({
    stdin: {
      contents: `
        import React from 'react'; import {createRoot} from 'react-dom/client';
        import {App} from './App.tsx'; import './fonts.css'; import './style.css';
        const user={session:1,name:'quality-fixture',key:'voice.1',channelId:1,selfMute:false,selfDeaf:false,isSelf:true};
        const session=(id=1)=>({sessionId:1,channelId:id,revision:id,name:user.name,identity:'voice.1',
          voice:{url:'wss://fixture.invalid',token:'fixture-only',identity:'voice.1'},screen:{url:'wss://fixture.invalid',token:'fixture-only',identity:'screen.1'}});
        const tree={id:1,name:'Общий',position:0,users:[user],children:[{id:2,name:'Игра',position:1,users:[],children:[]}]};
        window.__qualityStats={captures:[],grants:0,stops:0};
        window.gul={servers:async()=>({servers:[],storage:'protected',lastSave:null}),
          captureCapabilities:async()=>({platform:'fixture',details:'Синтетический UI тест',systemAudio:false}),
          appInfo:async()=>({version:'fixture'}),onPushToTalk:()=>()=>{},onCapturePicker:()=>()=>{},
          connect:async()=>session(),channel:async(id)=>session(id),disconnect:async()=>{},
          state:async()=>({tree,selfSession:1,selfChannel:1,revision:1}),
          screen:async()=>{window.__qualityStats.grants++;return {identity:'screen.1'};},
          audio:async(state)=>state,setPushToTalk:async()=>{},recordDiagnostic:async()=>{},
          minimize:async()=>{},maximize:async()=>{},closeWindow:async()=>{}};
        localStorage.setItem('gul.address','livekit+vless://fixture.invalid');
        localStorage.setItem('gul.username','quality-fixture');
        createRoot(document.getElementById('root')).render(<App/>);
      `,
      resolveDir: renderer,
      loader: 'tsx',
    },
    plugins: [
      {
        name: 'synthetic-media-only',
        setup(builder) {
          builder.onResolve({ filter: /\/media\/controller\.ts$/ }, () => ({
            path: 'fixture-controller',
            namespace: 'fixture',
          }));
          builder.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({
            contents: `
              import {initialSnapshot} from './media/model.ts';
              export class MediaController {
                snapshot=initialSnapshot(); listeners=new Set();
                constructor(){window.__dropQualityMedia=()=>this.update({state:'disconnected'});
                  window.__omitQualityMetadata=()=>this.update({screenQuality:null});}
                getSnapshot=()=>this.snapshot;
                subscribe=(fn)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn);};
                update=(fields)=>{this.snapshot={...this.snapshot,...fields};this.listeners.forEach(fn=>fn());};
                join=async()=>{this.update({state:'connected'});
                  if(window.__holdQualityJoin)await new Promise(resolve=>{window.__finishQualityJoin=resolve;});};
                leave=async()=>this.update({state:'disconnected',sharing:false,screenQuality:null});
                setVoiceSettings=async(voiceSettings)=>this.update({voiceSettings});
                setDevice=async()=>{}; setAudio=async(fields)=>this.update(fields);
                setUserVolume=()=>{};setUserMuted=()=>{};sendChat=async()=>{};watchScreen=async()=>{};
                startScreen=async(grant,audio,quality)=>{
                  window.__qualityStats.captures.push({quality,audio,gesture:navigator.userActivation.isActive});
                  await grant;this.update({sharing:true,screenQuality:quality,screenAudio:'off'});
                };
                stopScreen=async()=>{window.__qualityStats.stops++;this.update({sharing:false,screenQuality:null});};
              }
            `,
            resolveDir: renderer,
            loader: 'ts',
          }));
        },
      },
    ],
    bundle: true,
    platform: 'browser',
    format: 'esm',
    jsx: 'automatic',
    outfile: join(directory, 'ui.js'),
    loader: { '.woff2': 'file', '.png': 'file' },
    define: { 'process.env.NODE_ENV': '"production"' },
  });
  await writeFile(
    join(directory, 'index.html'),
    `<!doctype html><html lang="ru"><head>
    <meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; connect-src 'none'">
    <link rel="stylesheet" href="ui.css"></head><body><div id="root"></div><script type="module" src="ui.js"></script></body></html>`,
  );
  await writeFile(
    join(directory, 'main.cjs'),
    `const {app,BrowserWindow,session}=require('electron'); const path=require('node:path');
    app.whenReady().then(()=>{session.defaultSession.setPermissionRequestHandler((_wc,_p,respond)=>respond(false));
      const window=new BrowserWindow({width:900,height:560,useContentSize:true,frame:false,
        webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
      window.loadFile(path.join(__dirname,'index.html'));});
    app.on('window-all-closed',()=>app.quit());`,
  );
  const app = await electron.launch({
    executablePath: require('electron'),
    args: [join(directory, 'main.cjs'), `--user-data-dir=${join(directory, 'profile')}`],
  });
  try {
    const page = await app.firstWindow();
    await page.evaluate(() => {
      (window as any).__holdQualityJoin = true;
    });
    await page.getByLabel('Пароль', { exact: true }).fill('fixture-only');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    const show = page.getByRole('button', { name: 'Показать экран', exact: true });
    await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible();
    await expect(show).toBeDisabled();
    await expect(page.getByRole('dialog', { name: 'Начать демонстрацию', exact: true })).toHaveCount(0);
    await page.evaluate(() => {
      (window as any).__holdQualityJoin = false;
      (window as any).__finishQualityJoin();
    });
    await expect(show).toBeEnabled();
    await show.click();
    const dialog = page.getByRole('dialog', { name: 'Начать демонстрацию', exact: true });
    await expect(dialog).toBeVisible();
    const select = dialog.getByRole('combobox', { name: 'Качество демонстрации', exact: true });
    await expect(select).toHaveValue('720p30');
    expect(
      await select
        .locator('option')
        .evaluateAll((options) => options.map((item) => item.getAttribute('value'))),
    ).toEqual(['720p30', '720p60', '1080p30', '1080p60']);
    await expect(dialog.getByRole('checkbox')).toHaveCount(0);
    await select.selectOption('1080p60');
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    expect(await page.evaluate(() => (window as any).__qualityStats)).toEqual({
      captures: [],
      grants: 0,
      stops: 0,
    });
    await show.click();
    await expect(select).toHaveValue('720p30');
    await select.selectOption('1080p60');
    await dialog.getByRole('button', { name: 'Выбрать экран', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    const stop = page.getByRole('button', { name: 'Остановить демонстрацию', exact: true });
    await expect(stop).toHaveAttribute('title', /1080p.*60/u);
    expect(await page.evaluate(() => (window as any).__qualityStats.captures)).toEqual([
      { quality: '1080p60', audio: true, gesture: true },
    ]);
    await expect
      .poll(() => page.evaluate(() => JSON.parse(localStorage.getItem('gul.preferences')!).screenQuality))
      .toBe('1080p60');
    await page.getByRole('button', { name: 'Настройки', exact: true }).click();
    const settings = page.getByRole('dialog', { name: 'Настройки', exact: true });
    await settings.getByRole('tab', { name: 'Демонстрация', exact: true }).click();
    await settings
      .getByRole('combobox', { name: 'Качество демонстрации', exact: true })
      .selectOption('720p60');
    await expect
      .poll(() => page.evaluate(() => JSON.parse(localStorage.getItem('gul.preferences')!).screenQuality))
      .toBe('720p60');
    expect(await settings.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await page.keyboard.press('Escape');
    await expect(stop).toHaveAttribute('title', /1080p.*60/u);
    await page.evaluate(() => (window as any).__omitQualityMetadata());
    await expect(stop).toHaveAttribute('title', 'Остановить демонстрацию');
    await stop.click();
    await expect(dialog).toHaveCount(0);
    await show.click();
    await expect(select).toHaveValue('720p60');
    await dialog.getByRole('button', { name: 'Отмена', exact: true }).click();
    await show.click();
    await page.evaluate(() => (window as any).__dropQualityMedia());
    await expect(dialog).toHaveCount(0);
    expect(await page.evaluate(() => (window as any).__qualityStats.captures.length)).toBe(1);
    await page.getByRole('button', { name: 'Отключиться', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    await page.getByLabel('Пароль', { exact: true }).fill('fixture-only');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    for (const quality of ['720p30', '720p60', '1080p30', '1080p60']) {
      await show.click();
      await select.selectOption(quality);
      await dialog.getByRole('button', { name: 'Выбрать экран', exact: true }).click();
      await expect(stop).toBeVisible();
      await stop.click();
    }
    expect(
      await page.evaluate(() =>
        (window as any).__qualityStats.captures.slice(1).map((item: any) => item.quality),
      ),
    ).toEqual(['720p30', '720p60', '1080p30', '1080p60']);
    await page.reload();
    await page.getByLabel('Пароль', { exact: true }).fill('fixture-only');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(show).toBeEnabled();
    await show.click();
    await expect(select).toHaveValue('1080p60');
    await dialog.getByRole('button', { name: 'Отмена', exact: true }).click();
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});
