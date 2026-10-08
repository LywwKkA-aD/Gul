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
