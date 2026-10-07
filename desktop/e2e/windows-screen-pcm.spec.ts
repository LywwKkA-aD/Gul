import { test, expect, _electron as electron } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { WindowsAudioBridge } from '../src/main/windows-audio-bridge.ts';

const require = createRequire(import.meta.url);

/** Synthetic PCM tests the real bridge, sandbox, worklet and track without recording any desktop. */
test('native Windows PCM reaches a real Electron stereo track and closes without a repeated audio tail', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-windows-pcm-'));
  let valid = true;
  let bridgeFailed = false;
  const bridge = new WindowsAudioBridge(
    'a'.repeat(48),
    () => valid,
    () => {
      bridgeFailed = true;
    },
  );
  const url = await bridge.listen();
  let sequence = 0;
  const started = performance.now();
  const timer = setInterval(() => {
    // Match hardware sample time rather than accumulating setInterval drift.
    const due = Math.floor((performance.now() - started) / 10);
    while (sequence < due) {
      const frame = Buffer.alloc(16 + 480 * 8);
      frame.writeUInt32LE(0x314c5547, 0);
      frame.writeUInt32LE(sequence, 4);
      frame.writeUInt32LE(480, 8);
      for (let index = 0; index < 480; index++) {
        const sample = sequence * 480 + index;
        frame.writeFloatLE(0.2 * Math.sin((2 * Math.PI * 440 * sample) / 48000), 16 + index * 8);
        frame.writeFloatLE(0.2 * Math.sin((2 * Math.PI * 660 * sample) / 48000), 20 + index * 8);
      }
      ++sequence;
      bridge.send(frame);
    }
  }, 4);
  let app: Awaited<ReturnType<typeof electron.launch>> | undefined;
  try {
    await build({
      stdin: {
        contents: `
          import {openWindowsAudio} from './media/windows-screen-audio.ts';
          let source,context,input,splitter,analysers,ended=0;
          window.__gulPCMProof={
            async start(){
              source=await openWindowsAudio(${JSON.stringify(url)},()=>{++ended});
              context=new AudioContext({sampleRate:48000});
              input=context.createMediaStreamSource(new MediaStream([source.track.mediaStreamTrack]));
              splitter=context.createChannelSplitter(2);input.connect(splitter);
              analysers=[context.createAnalyser(),context.createAnalyser()];
              analysers.forEach((analyser,channel)=>{analyser.fftSize=4096;splitter.connect(analyser,channel)});
              await context.resume();
            },
            inspect(){
              if(!source)return null;
              const spectrum=analysers.map(analyser=>{
                const values=new Float32Array(analyser.frequencyBinCount);analyser.getFloatFrequencyData(values);
                return [440,660].map(frequency=>{
                  const bin=Math.round(frequency*analyser.fftSize/context.sampleRate);
                  return Math.max(...values.slice(bin-2,bin+3));
                });
              });
              return {ended,live:source.track.mediaStreamTrack.readyState==='live',
                channels:source.track.mediaStreamTrack.getSettings().channelCount,
                constraints:source.track.mediaStreamTrack.getConstraints(),
                separation:Math.min(spectrum[0][0]-spectrum[0][1],spectrum[1][1]-spectrum[1][0]),
                energy:Math.min(spectrum[0][0],spectrum[1][1])};
            },
            async stop(){await source.close();input.disconnect();splitter.disconnect();await context.close();},
          };
          document.getElementById('start').onclick=()=>window.__gulPCMProof.start()
            .then(()=>document.getElementById('status').textContent='ready')
            .catch(()=>document.getElementById('status').textContent='failed');
        `,
        resolveDir: fileURLToPath(new URL('../src/renderer', import.meta.url)),
        loader: 'ts',
      },
      bundle: true,
      platform: 'browser',
      format: 'esm',
      target: 'chrome152',
      outfile: join(directory, 'proof.js'),
      define: { 'process.env.NODE_ENV': '"production"' },
    });
    await build({
      entryPoints: [fileURLToPath(new URL('../src/renderer/media/screen-audio-worklet.ts', import.meta.url))],
      bundle: true,
      platform: 'browser',
      format: 'iife',
      target: 'chrome152',
      outfile: join(directory, 'screen-audio-worklet.js'),
    });
    await writeFile(
      join(directory, 'index.html'),
      '<!doctype html><html><body><button id="start">Start synthetic PCM</button><output id="status">idle</output><script type="module" src="proof.js"></script></body></html>',
    );
    await build({
      stdin: {
        contents: `
          import {app,BrowserWindow,protocol,session} from 'electron';
          import {installAppProtocol} from './protocol.ts';
          import {installPermissions} from './permissions.ts';
          import {appAsset} from './security.ts';
          protocol.registerSchemesAsPrivileged([{scheme:'gul',privileges:{standard:true,secure:true,supportFetchAPI:true,corsEnabled:true,stream:true}}]);
          app.whenReady().then(()=>{
            installAppProtocol(session.defaultSession,${JSON.stringify(directory)});
            const window=new BrowserWindow({width:600,height:400,webPreferences:{sandbox:true,contextIsolation:true,nodeIntegration:false,webSecurity:true}});
            installPermissions(window,{connected:()=>true,networkAllowed:(value)=>Boolean(appAsset(value))},
              (value)=>value===${JSON.stringify(url)});
            window.loadURL('gul://app/index.html');
          });
          app.on('window-all-closed',()=>app.quit());
        `,
        resolveDir: fileURLToPath(new URL('../src/main', import.meta.url)),
        loader: 'ts',
      },
      bundle: true,
      platform: 'node',
      format: 'cjs',
      target: 'node24',
      external: ['electron'],
      outfile: join(directory, 'main.cjs'),
    });
    app = await electron.launch({
      executablePath: require('electron'),
      args: [join(directory, 'main.cjs'), `--user-data-dir=${join(directory, 'profile')}`],
    });
    const page = await app.firstWindow();
    await page.getByRole('button', { name: 'Start synthetic PCM' }).click();
    await expect(page.locator('#status')).toHaveText('ready', { timeout: 10_000 });
    const inspect = () =>
      page.evaluate(() =>
        (
          window as unknown as {
            __gulPCMProof: {
              inspect(): {
                ended: number;
                live: boolean;
                channels: number;
                separation: number;
                energy: number;
                constraints: MediaTrackConstraints;
              };
            };
          }
        ).__gulPCMProof.inspect(),
      );
    await expect
      .poll(
        async () => {
          const sample = await inspect();
          return sample.energy > -35 && sample.separation > 30 && sample.ended === 0;
        },
        { timeout: 10_000 },
      )
      .toBe(true);
    const measured = await inspect();
    expect(measured.energy).toBeGreaterThan(-35);
    expect(measured.live).toBe(true);
    expect(measured.channels).toBe(2);
    expect(measured.ended).toBe(0);
    expect(bridgeFailed).toBe(false);
    clearInterval(timer);
    valid = false;
    await bridge.close();
    await expect.poll(async () => (await inspect()).ended).toBe(1);
    await expect.poll(async () => (await inspect()).live).toBe(false);
    await page.evaluate(() =>
      (window as unknown as { __gulPCMProof: { stop(): Promise<void> } }).__gulPCMProof.stop(),
    );
  } finally {
    clearInterval(timer);
    valid = false;
    await bridge.close();
    await app?.close();
    await rm(directory, { recursive: true, force: true });
  }
});
