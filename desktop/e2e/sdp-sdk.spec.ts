import { test, expect, _electron as electron } from '@playwright/test';
import { build } from 'esbuild';
import { createRequire } from 'node:module';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const require = createRequire(import.meta.url);

test('pinned LiveKit transport conforms the first offer before Chromium assigns transceiver mids', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'gul-sdp-sdk-'));
  const app = await electron.launch({
    executablePath: require('electron'),
    args: ['.', '--gul-electron-test', `--user-data-dir=${directory}`],
    env: { ...process.env, NODE_ENV: 'test' },
  });
  try {
    const page = await app.firstWindow();
    await expect(page.getByRole('button', { name: 'Подключиться', exact: true })).toBeVisible();
    const runtime = await build({
      stdin: {
        contents: `
          import PCTransport from './node_modules/livekit-client/src/room/PCTransport.ts';
          import Room from './node_modules/livekit-client/src/room/Room.ts';
          import { LogLevel, setLogLevel } from './node_modules/livekit-client/src/logger.ts';
          import { installBundleWorkaround } from './src/renderer/media/sdp-bundle.ts';
          setLogLevel(LogLevel.silent);
          globalThis.__gulSDK = { PCTransport, Room, installBundleWorkaround };
        `,
        resolveDir: process.cwd(),
      },
      bundle: true,
      format: 'iife',
      platform: 'browser',
      write: false,
    });
    await page.evaluate(runtime.outputFiles[0].text);
    const result = await page.evaluate(async () => {
      interface Transport {
        onOffer?: (offer: RTCSessionDescriptionInit, id: number) => void;
        addTransceiverOfKind(kind: 'video', init: RTCRtpTransceiverInit): RTCRtpTransceiver;
        addTransceiver(track: MediaStreamTrack, init: RTCRtpTransceiverInit): RTCRtpTransceiver;
        setTrackCodecBitrate(info: {
          cid: string;
          codec: string;
          maxbr: number;
          isScreenShare: boolean;
        }): void;
        setMungedSDP(sd: RTCSessionDescriptionInit, munged?: string, remote?: boolean): Promise<void>;
        getTransceivers(): RTCRtpTransceiver[];
        createAndSendOffer(): Promise<void>;
        setRemoteDescription(sd: RTCSessionDescriptionInit, id: number): Promise<void>;
        close(): void;
      }
      const sdk = (
        globalThis as unknown as {
          __gulSDK: {
            PCTransport: new (options: RTCConfiguration) => Transport;
            Room: new () => { engine: { emit(event: string, transport: Transport): void } };
            installBundleWorkaround(room: unknown): void;
          };
        }
      ).__gulSDK;
      const run = async (repair: boolean) => {
        const publisher = new sdk.PCTransport({});
        const room = new sdk.Room();
        const receiver = new RTCPeerConnection();
        const canvas = document.createElement('canvas');
        canvas.width = 1280;
        canvas.height = 720;
        canvas.getContext('2d')!.fillRect(0, 0, 1280, 720);
        const stream = canvas.captureStream(30);
        const placeholder = publisher.addTransceiverOfKind('video', { direction: 'recvonly' });
        const track = stream.getVideoTracks()[0];
        let active = publisher.addTransceiver(track, { direction: 'sendonly', streams: [stream] });
        const streams = [stream];
        const vp8 = RTCRtpSender.getCapabilities('video')!.codecs.filter(
          (codec) => codec.mimeType.toLowerCase() === 'video/vp8',
        );
        for (const transceiver of [placeholder, active]) transceiver.setCodecPreferences(vp8);
        publisher.setTrackCodecBitrate({ cid: track.id, codec: 'vp8', maxbr: 2000, isScreenShare: true });
        const attempts: {
          remote: boolean;
          conformed: boolean;
          hint: boolean;
          nullMids: number;
          retained: number;
        }[] = [];
        const original = publisher.setMungedSDP;
        publisher.setMungedSDP = function (sd, munged, remote) {
          if ((!remote && sd.type === 'offer') || (remote && sd.type === 'answer')) {
            const sdp = munged ?? sd.sdp ?? '';
            const configs = sdp
              .split(/(?=m=)/)
              .filter((part) => part.startsWith('m=video'))
              .map((part) => /^a=fmtp:96 (.+)$/m.exec(part)?.[1].trim() ?? '');
            attempts.push({
              remote: Boolean(remote),
              conformed: configs.length > 1 && configs.every((config) => config === configs[0]),
              hint: sdp.includes('x-google-start-bitrate=1800'),
              nullMids: this.getTransceivers().filter((transceiver) => transceiver.mid === null).length,
              retained: this.getTransceivers().filter(
                (transceiver) => transceiver.sender.track?.readyState === 'ended',
              ).length,
            });
          }
          return original.call(this, sd, munged, remote);
        };
        if (repair) {
          sdk.installBundleWorkaround(room);
          room.engine.emit('transportsCreated', publisher);
        }
        let answer: Promise<void> | undefined;
        publisher.onOffer = (offer, id) => {
          answer = (async () => {
            await receiver.setRemoteDescription(offer);
            const description = await receiver.createAnswer();
            // Match the proven SFU answer: the published track carries its encoder
            // hint while the pre-populated recvonly publisher section has no fmtp.
            const parts = description.sdp!.split(/(?=m=)/).map((part) => {
              if (!part.startsWith('m=video')) return part;
              const mid = /^a=mid:(.+)$/m.exec(part)?.[1].trim();
              const withoutHint = part.replace(/^a=fmtp:96 x-google-start-bitrate=1800\r?\n/gm, '');
              return mid === active.mid
                ? withoutHint + 'a=fmtp:96 x-google-start-bitrate=1800\r\n'
                : withoutHint;
            });
            const serverAnswer = { type: 'answer' as const, sdp: parts.join('') };
            await receiver.setLocalDescription(serverAnswer);
            await publisher.setRemoteDescription(serverAnswer, id);
          })();
        };
        try {
          await publisher.createAndSendOffer();
          await answer;
          const firstAnswer = attempts.find((attempt) => attempt.remote)!;
          track.stop();
          active.direction = 'inactive';
          const restarted = canvas.captureStream(30);
          streams.push(restarted);
          const restartedTrack = restarted.getVideoTracks()[0];
          active = publisher.addTransceiver(restartedTrack, { direction: 'sendonly', streams: [restarted] });
          active.setCodecPreferences(vp8);
          publisher.setTrackCodecBitrate({
            cid: restartedTrack.id,
            codec: 'vp8',
            maxbr: 2000,
            isScreenShare: true,
          });
          await publisher.createAndSendOffer();
          await answer;
          return {
            first: attempts[0],
            answer: firstAnswer,
            restart: attempts.find((attempt) => !attempt.remote && attempt.retained > 0)!,
            restartAnswer: attempts.at(-1)!,
          };
        } finally {
          streams.forEach((captured) => captured.getTracks().forEach((mediaTrack) => mediaTrack.stop()));
          publisher.close();
          receiver.close();
        }
      };
      return { before: await run(false), after: await run(true) };
    });
    console.info('GUL_SDP_SDK_FIRST_OFFER', JSON.stringify(result));
    expect(result.before.first.nullMids).toBe(2);
    expect(result.before.first.conformed).toBe(false);
    expect(result.before.answer.conformed).toBe(false);
    expect(result.after.first.nullMids).toBe(2);
    expect(result.after.first.hint).toBe(true);
    expect(result.after.first.conformed).toBe(true);
    expect(result.after.answer.conformed).toBe(true);
    expect(result.after.answer.hint).toBe(true);
    expect(result.after.restart.retained).toBe(1);
    expect(result.after.restart.conformed).toBe(true);
    expect(result.after.restart.hint).toBe(true);
    expect(result.after.restartAnswer.conformed).toBe(true);
  } finally {
    await app.close();
    await rm(directory, { recursive: true, force: true });
  }
});
