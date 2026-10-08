import { test, expect, _electron as electron } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
const require = createRequire(import.meta.url);
test('owner channel controls retain media, enforce protected channels and hide management for members', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-channel-ui-')),
    renderer = fileURLToPath(new URL('../src/renderer', import.meta.url));
  await build({
    stdin: {
      contents: `
 import React from'react';import{createRoot}from'react-dom/client';import{App}from'./App.tsx';import'./fonts.css';import'./style.css';
 const serverId='a'.repeat(32),ownerId='b'.repeat(32),memberId='c'.repeat(32);let role='owner',catalogVersion=1;
 const member=()=>({id:role==='guest'?null:role==='owner'?ownerId:memberId,role});
 const user={session:1,name:'owner-fixture',key:'voice.1',channelId:1,selfMute:false,selfDeaf:false,isSelf:true};
 const node=(id,name,users=[])=>({id,name,position:id,version:1,access:'open',canJoin:true,users,children:[]});
 let tree={...node(0,'Root'),children:[node(1,'Общий',[user]),{...node(2,'Закрытый'),access:'restricted',canJoin:false}]};
 const state=()=>({tree,selfSession:1,selfChannel:1,revision:1,serverId,member:member(),catalogVersion});
 const session=()=>({epoch:1,sessionId:1,channelId:1,revision:1,name:user.name,identity:'voice.1',serverId,member:member(),catalogVersion});
 let credential={state:'none',serverId:null,memberId:null,rememberIdentity:false,usable:false};
 window.__management={captures:0,joins:0,leaves:0,changes:[],imports:0,redeems:0};window.__role=(value)=>{role=value;};
 window.gul={servers:async()=>({servers:[],storage:'protected',lastSave:null}),captureCapabilities:async()=>({platform:'fixture',details:'Synthetic',systemAudio:false}),appInfo:async()=>({version:'fixture'}),onPushToTalk:()=>()=>{},onCapturePicker:()=>()=>{},
 connect:async()=>session(),channel:async()=>session(),disconnect:async()=>{},state:async()=>state(),audio:async(v)=>v,setPushToTalk:async()=>{},recordDiagnostic:async()=>{},minimize:async()=>{},maximize:async()=>{},closeWindow:async()=>{},
 memberCredential:async()=>credential,importMemberCredential:async({rememberIdentity})=>{window.__management.imports++;return credential={state:'loaded',serverId,memberId:ownerId,rememberIdentity,usable:true};},setMemberCredentialConsent:async({rememberIdentity})=>credential={...credential,rememberIdentity},clearMemberCredential:async()=>{credential={state:'none',serverId:null,memberId:null,rememberIdentity:false,usable:false};},
 redeemInvitation:async(value)=>{window.__management.redeems++;return credential={state:'saved',serverId,memberId,rememberIdentity:value.rememberIdentity,usable:true};},
 members:async()=>({catalogVersion,members:[{id:ownerId,name:'Владелец',role:'owner',revoked:false},{id:memberId,name:'Друг',role:'member',revoked:false}]}),
 channelPermissions:async({channelId})=>({...tree.children.find(c=>c.id===channelId),channelId,allowedMemberIds:[]}),
 createChannel:async(v)=>{window.__management.changes.push(v);tree={...tree,children:[...tree.children,{...node(3,v.name),access:v.access,allowedMemberIds:v.allowedMemberIds}]};catalogVersion++;return state();},
 updateChannel:async(v)=>{window.__management.changes.push(v);tree={...tree,children:tree.children.map(c=>c.id===v.channelId?{...c,...v,version:c.version+1}:c)};catalogVersion++;return state();},
 deleteChannel:async(v)=>{window.__management.changes.push(v);tree={...tree,children:tree.children.filter(c=>c.id!==v.channelId)};catalogVersion++;return state();},
 createInvitation:async()=>({inviteToken:'d'.repeat(43),expiresAtUnixSeconds:2000000000})};
 localStorage.setItem('gul.address','livekit+vless://fixture.invalid');localStorage.setItem('gul.username','owner-fixture');
 createRoot(document.getElementById('root')).render(<App/>);
 `,
      resolveDir: renderer,
      loader: 'tsx',
    },
    plugins: [
      {
        name: 'no-media-fixture',
        setup(b) {
          b.onResolve({ filter: /\/media\/controller\.ts$/ }, () => ({
            path: 'fixture',
            namespace: 'fixture',
          }));
          b.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({
            contents: `import{initialSnapshot}from'./media/model.ts';export class MediaController{snapshot=initialSnapshot();listeners=new Set();getSnapshot=()=>this.snapshot;subscribe=(fn)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn);};update=(v)=>{this.snapshot={...this.snapshot,...v};this.listeners.forEach(fn=>fn());};join=async()=>{window.__management.joins++;this.update({state:'connected'});};leave=async()=>{window.__management.leaves++;this.update({state:'disconnected'});};setVoiceSettings=async()=>{};setDevice=async()=>{};setAudio=async(v)=>this.update(v);setUserVolume=()=>{};setUserMuted=()=>{};sendChat=async()=>{};watchScreen=async()=>{};startScreen=async()=>{window.__management.captures++;};stopScreen=async()=>{};}`,
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
    `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; connect-src 'none'"><link rel="stylesheet" href="ui.css"></head><body><div id="root"></div><script type="module" src="ui.js"></script></body></html>`,
  );
  await writeFile(
    join(directory, 'main.cjs'),
    `const{app,BrowserWindow,session}=require('electron');const path=require('node:path');app.whenReady().then(()=>{session.defaultSession.setPermissionRequestHandler((_w,_p,respond)=>respond(false));const w=new BrowserWindow({width:900,height:560,useContentSize:true,frame:false,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});w.loadFile(path.join(__dirname,'index.html'));});app.on('window-all-closed',()=>app.quit());`,
  );
  const app = await electron.launch({
    executablePath: require('electron'),
    args: [join(directory, 'main.cjs'), `--user-data-dir=${join(directory, 'profile')}`],
  });
  try {
    const page = await app.firstWindow();
    await page.getByText('Личный доступ к серверу', { exact: true }).click();
    await page.getByRole('button', { name: 'Загрузить личный ключ', exact: true }).click();
    await expect(page.getByText('Личный ключ загружен', { exact: true })).toBeVisible();
    const identityConsent = page.getByLabel('Запомнить личный ключ на этом компьютере', { exact: true });
    await identityConsent.check();
    await expect(identityConsent).toBeChecked();
    await identityConsent.uncheck();
    await expect(identityConsent).not.toBeChecked();
    await page.getByRole('button', { name: 'Принять приглашение', exact: true }).click();
    await page.keyboard.press('Escape');
    expect(await page.evaluate(() => (window as any).__management.redeems)).toBe(0);
    await page.getByLabel('Пароль', { exact: true }).fill('fixture-only');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Закрытый', exact: true })).toBeDisabled();
    await page.getByRole('button', { name: 'Управление каналами', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Управление каналами', exact: true });
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: 'Новый канал', exact: true }).click();
    await dialog.getByLabel('Название канала', { exact: true }).fill('Команда');
    await dialog.getByLabel('Доступ к каналу', { exact: true }).selectOption('restricted');
    await dialog.getByLabel('Друг', { exact: true }).check();
    await dialog.getByRole('button', { name: 'Создать канал', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Команда', exact: true })).toBeVisible();
    await dialog.getByLabel('Редактируемый канал', { exact: true }).selectOption('3');
    await dialog.getByRole('button', { name: 'Редактировать', exact: true }).click();
    await dialog.getByLabel('Название канала', { exact: true }).fill('Команда 2');
    await dialog.getByRole('button', { name: 'Сохранить канал', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Команда 2', exact: true })).toBeVisible();
    await dialog.getByLabel('Редактируемый канал', { exact: true }).selectOption('1');
    await expect(dialog.getByRole('button', { name: 'Удалить канал', exact: true })).toBeDisabled();
    await dialog.getByLabel('Редактируемый канал', { exact: true }).selectOption('3');
    await dialog.getByRole('button', { name: 'Удалить канал', exact: true }).click();
    await dialog.getByRole('button', { name: 'Подтвердить удаление', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Команда 2', exact: true })).toHaveCount(0);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Пригласить участника', exact: true }).click();
    const invite = page.getByRole('dialog', { name: 'Приглашение участника', exact: true });
    await invite.getByRole('button', { name: 'Создать приглашение', exact: true }).click();
    await expect(invite.getByLabel('Код приглашения', { exact: true })).toHaveValue('d'.repeat(43));
    await page.keyboard.press('Escape');
    expect(await page.evaluate(() => (window as any).__management)).toMatchObject({
      captures: 0,
      joins: 1,
      leaves: 1,
      imports: 1,
    });
    expect(await page.evaluate(() => (window as any).__management.changes[0])).toMatchObject({
      access: 'restricted',
      allowedMemberIds: ['c'.repeat(32)],
      epoch: 1,
      serverId: 'a'.repeat(32),
    });
    await page.getByRole('button', { name: 'Отключиться', exact: true }).click();
    await page.evaluate(() => (window as any).__role('member'));
    await page.getByLabel('Пароль', { exact: true }).fill('fixture-only');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByText('Голос подключён', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Управление каналами', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Пригласить участника', exact: true })).toHaveCount(0);
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});

test('channel transitions ignore transient and older null polls but stable revocation disconnects', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-channel-poll-'));
  const renderer = fileURLToPath(new URL('../src/renderer', import.meta.url));
  await build({
    stdin: {
      contents: `
        import React from 'react';import {createRoot} from 'react-dom/client';
        import {App} from './App.tsx';import './fonts.css';import './style.css';
        let channelId=1,epoch=1,mode='ok';let finishChannel,finishPoll,pollCallback;
        const counters={joins:0,leaves:0,disconnects:0,channelRequests:0,pollRequests:0,nullReplies:0};
        window.__pollFacts=counters;
        const nativeInterval=window.setInterval.bind(window),nativeClear=window.clearInterval.bind(window);
        window.setInterval=(callback,delay,...args)=>{
          if(delay===1000){pollCallback=()=>callback(...args);return 2147483600;}
          return nativeInterval(callback,delay,...args);
        };
        window.clearInterval=(id)=>{if(id!==2147483600)nativeClear(id);};
        const user=()=>({session:1,name:'poll-fixture',key:'voice.1',channelId,selfMute:false,selfDeaf:false,isSelf:true});
        const node=(id,name)=>({id,name,position:id,users:channelId===id?[user()]:[],children:[]});
        const state=()=>({tree:{...node(0,'Root'),children:[node(1,'Общий'),node(2,'Игра')]},selfSession:1,selfChannel:channelId,revision:epoch});
        const session=()=>({epoch,sessionId:1,channelId,revision:epoch,name:'poll-fixture',identity:'voice.1'});
        window.__pollControl={
          mode:(value)=>{mode=value;},run:()=>{if(!pollCallback)throw Error('No app poll installed');pollCallback();},
          releaseChannel:()=>{if(!finishChannel)throw Error('No held channel');const finish=finishChannel;finishChannel=undefined;finish();},
          releasePoll:()=>{if(!finishPoll)throw Error('No held poll');const finish=finishPoll;finishPoll=undefined;finish(null);}
        };
        window.gul={
          servers:async()=>({servers:[],storage:'protected',lastSave:null}),
          memberCredential:async()=>({state:'none',serverId:null,memberId:null,rememberIdentity:false,usable:false}),
          captureCapabilities:async()=>({platform:'fixture',details:'Synthetic',systemAudio:false}),
          appInfo:async()=>({version:'fixture'}),onPushToTalk:()=>()=>{},onCapturePicker:()=>()=>{},
          connect:async()=>session(),channel:async(id)=>{counters.channelRequests++;await new Promise(resolve=>{finishChannel=resolve;});channelId=id;epoch++;return session();},
          disconnect:async()=>{counters.disconnects++;},state:async()=>{
            counters.pollRequests++;
            if(mode==='held'){const result=await new Promise(resolve=>{finishPoll=resolve;});if(result===null)counters.nullReplies++;return result;}
            if(mode==='null'){counters.nullReplies++;return null;}return state();
          },audio:async(value)=>value,setPushToTalk:async()=>{},recordDiagnostic:async()=>{},
          minimize:async()=>{},maximize:async()=>{},closeWindow:async()=>{}
        };
        localStorage.setItem('gul.address','livekit+vless://fixture.invalid');localStorage.setItem('gul.username','poll-fixture');
        createRoot(document.getElementById('root')).render(<App/>);
      `,
      resolveDir: renderer,
      loader: 'tsx',
    },
    plugins: [
      {
        name: 'no-media-poll-fixture',
        setup(builder) {
          builder.onResolve({ filter: /\/media\/controller\.ts$/ }, () => ({
            path: 'fixture',
            namespace: 'fixture',
          }));
          builder.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({
            contents: `import{initialSnapshot}from'./media/model.ts';export class MediaController{
            snapshot=initialSnapshot();listeners=new Set();getSnapshot=()=>this.snapshot;
            subscribe=(fn)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn);};
            update=(value)=>{this.snapshot={...this.snapshot,...value};this.listeners.forEach(fn=>fn());};
            join=async()=>{window.__pollFacts.joins++;this.update({state:'connected'});};
            leave=async()=>{window.__pollFacts.leaves++;this.update({state:'disconnected'});};
            setVoiceSettings=async()=>{};setDevice=async()=>{};setAudio=async(value)=>this.update(value);
            setUserVolume=()=>{};setUserMuted=()=>{};sendChat=async()=>{};watchScreen=async()=>{};startScreen=async()=>{};stopScreen=async()=>{};
          }`,
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
    `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; connect-src 'none'"><link rel="stylesheet" href="ui.css"></head><body><div id="root"></div><script type="module" src="ui.js"></script></body></html>`,
  );
  await writeFile(
    join(directory, 'main.cjs'),
    `const{app,BrowserWindow,session}=require('electron');const path=require('node:path');app.whenReady().then(()=>{session.defaultSession.setPermissionRequestHandler((_w,_p,respond)=>respond(false));const window=new BrowserWindow({width:900,height:560,useContentSize:true,frame:false,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false}});window.loadFile(path.join(__dirname,'index.html'));});app.on('window-all-closed',()=>app.quit());`,
  );
  const app = await electron.launch({
    executablePath: require('electron'),
    args: [join(directory, 'main.cjs'), `--user-data-dir=${join(directory, 'profile')}`],
  });
  try {
    const page = await app.firstWindow();
    const flush = () =>
      page.evaluate(
        () =>
          new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))),
      );
    const control = (mode: string) =>
      page.evaluate((value) => (window as any).__pollControl.mode(value), mode);
    const runPoll = () => page.evaluate(() => (window as any).__pollControl.run());
    const channel = async (name: string, request: number) => {
      await page.getByRole('button', { name, exact: true }).click();
      await expect.poll(() => page.evaluate(() => (window as any).__pollFacts.channelRequests)).toBe(request);
    };
    const finish = async (name: string) => {
      await control('ok');
      await page.evaluate(() => (window as any).__pollControl.releaseChannel());
      await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
      await expect(page.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
    };
    await page.getByLabel('Пароль', { exact: true }).fill('fixture-only');
    await page.getByRole('button', { name: 'Подключиться', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Показать экран', exact: true })).toBeEnabled();
    await runPoll();
    await expect.poll(() => page.evaluate(() => (window as any).__pollFacts.pollRequests)).toBeGreaterThan(0);

    // A poll fired after channel() starts must not treat its temporarily absent active session as revocation.
    await channel('Игра', 1);
    await control('null');
    await runPoll();
    await flush();
    expect(await page.evaluate(() => (window as any).__pollFacts.disconnects)).toBe(0);
    await finish('Игра');

    // A reply issued in the previous stable channel is stale as soon as the next transition begins.
    await control('held');
    const requests = await page.evaluate(() => (window as any).__pollFacts.pollRequests);
    await runPoll();
    await expect.poll(() => page.evaluate(() => (window as any).__pollFacts.pollRequests)).toBe(requests + 1);
    await channel('Общий', 2);
    await page.evaluate(() => (window as any).__pollControl.releasePoll());
    await flush();
    expect(await page.evaluate(() => (window as any).__pollFacts.disconnects)).toBe(0);
    await finish('Общий');

    // An old response arriving after the new channel has finished must not invalidate that newer session either.
    await control('held');
    await runPoll();
    await flush();
    await channel('Игра', 3);
    await finish('Игра');
    await page.evaluate(() => (window as any).__pollControl.releasePoll());
    await flush();
    expect(await page.evaluate(() => (window as any).__pollFacts.disconnects)).toBe(0);
    expect(await page.evaluate(() => (window as any).__pollFacts.joins)).toBe(4);

    // Genuine revocation in a stable current session remains fail-closed.
    await control('null');
    await runPoll();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    await expect.poll(() => page.evaluate(() => (window as any).__pollFacts.disconnects)).toBe(1);
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});
