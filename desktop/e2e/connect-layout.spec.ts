import { test, expect, _electron as electron, type Locator } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);

async function expectInScrollViewport(element: Locator) {
  await element.scrollIntoViewIfNeeded();
  expect(
    await element.evaluate((target) => {
      const bounds = target.getBoundingClientRect();
      const area = document.querySelector('.connect-page')!.getBoundingClientRect();
      return bounds.top >= area.top && bounds.bottom <= Math.min(innerHeight, area.bottom);
    }),
  ).toBe(true);
}

test('locked password recovery and saved-server controls remain reachable at minimum window height', async () => {
  // Only synthetic connection data is rendered; this fixture cannot connect or unlock a real vault.
  const directory = await mkdtemp(join(tmpdir(), 'gul-connect-layout-'));
  await build({
    stdin: {
      contents: `
        import React, {useState} from 'react';
        import {createRoot} from 'react-dom/client';
        import {ConnectPanel} from './ConnectPanel.tsx';
        import {passwordStorageRecoveryMessage} from './saved-login.ts';
        import './fonts.css'; import './style.css';
        const address='livekit+vless://fixture.invalid';
        const saved={storage:'unavailable',lastSave:null,
          passwordStorage:{provider:'gnome',state:'locked',restartRequired:true},
          servers:[{address,username:'layout-fixture',lastUsed:0,hasPassword:false,
            rememberPassword:true,passwordStatus:'locked'}]};
        function Fixture(){
          const [result,setResult]=useState('none'),[busy,setBusy]=useState(false);
          return <div className="app">
            <header className="titlebar"><span className="wordmark">GUL</span></header>
            <ConnectPanel address={address} username="layout-fixture" password="fixture-only"
              remember={false} saved={saved} busy={busy} error="Тестовое сообщение об ошибке подключения."
              storageNotice={passwordStorageRecoveryMessage({state:'unavailable',restartRequired:true})}
              onAddress={()=>{}} onUsername={()=>{}} onPassword={()=>{}} onRemember={()=>{}}
              onChoose={()=>setResult('selected')} onForget={()=>setResult('forgotten')}
              onRefreshSaved={()=>setResult('refreshed')} onUnlockStorage={()=>setResult('unlocked')}
              onOpenStorage={()=>setResult('opened')} onConnect={()=>{setResult('connected');setBusy(true);}}
              onCancel={()=>{setResult('cancelled');setBusy(false);}}/>
            <output aria-label="Результат действия" hidden>{result}</output>
          </div>;
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
    `
    const {app,BrowserWindow,session}=require('electron');
    const path=require('node:path');
    app.whenReady().then(()=>{
      session.defaultSession.setPermissionRequestHandler((_webContents,_permission,respond)=>respond(false));
      const window=new BrowserWindow({width:850,height:560,useContentSize:true,frame:false,
        webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});
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
    await expect(page.getByRole('heading', { name: 'Заходи. Общайся.' })).toBeVisible();
    const layout = await page.locator('.connect-page').evaluate((area) => {
      const bounds = area.getBoundingClientRect();
      const logo = area.querySelector('.brand-symbol')!.getBoundingClientRect();
      return {
        viewportHeight: innerHeight,
        bounded: bounds.top >= 0 && bounds.bottom <= innerHeight,
        topReachable: logo.top >= bounds.top && logo.bottom <= bounds.bottom,
        scrollable: area.scrollHeight > area.clientHeight,
        noHorizontalOverflow: area.scrollWidth <= area.clientWidth,
      };
    });
    expect(layout).toEqual({
      viewportHeight: 560,
      bounded: true,
      topReachable: true,
      scrollable: true,
      noHorizontalOverflow: true,
    });
    const result = page.getByLabel('Результат действия');
    for (const [name, action] of [
      ['Повторить чтение сохранённого пароля', 'refreshed'],
      ['Разблокировать хранилище', 'unlocked'],
      ['Открыть «Пароли и ключи»', 'opened'],
      ['Выбрать сервер fixture.invalid', 'selected'],
      ['Удалить сервер fixture.invalid', 'forgotten'],
    ] as const) {
      const button = page.getByRole('button', { name, exact: true });
      await expectInScrollViewport(button);
      await button.click();
      await expect(result).toHaveText(action);
    }
    const connect = page.getByRole('button', { name: 'Подключиться', exact: true });
    await expectInScrollViewport(connect);
    await connect.click();
    await expect(result).toHaveText('connected');
    const cancel = page.getByRole('button', { name: 'Отменить подключение', exact: true });
    await expectInScrollViewport(cancel);
    await cancel.click();
    await expect(result).toHaveText('cancelled');
    await expectInScrollViewport(page.getByRole('textbox', { name: 'Адрес сервера', exact: true }));
    await page.getByRole('textbox', { name: 'Адрес сервера', exact: true }).focus();
    await page.keyboard.press('Tab');
    await expect(page.getByRole('textbox', { name: 'Твой ник', exact: true })).toBeFocused();
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});
