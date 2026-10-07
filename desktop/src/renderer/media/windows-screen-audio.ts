import { LocalAudioTrack } from 'livekit-client';
import type { ScreenAudioLease } from '../../shared/contracts.ts';
import type { ScreenCapture } from './model.ts';

export interface WindowsAudioSource {
  readonly track: LocalAudioTrack;
  readonly close: () => Promise<void>;
}
interface WindowsLease {
  readonly leaseId: string;
  readonly url: string;
}
export function windowsAudioLease(value: unknown): value is WindowsLease {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const lease = value as Record<string, unknown>;
  if (
    typeof lease.leaseId !== 'string' ||
    !/^[a-f0-9]{32}$/u.test(lease.leaseId) ||
    typeof lease.url !== 'string' ||
    'deviceLabel' in lease
  )
    return false;
  try {
    const url = new URL(lease.url);
    return (
      url.protocol === 'ws:' &&
      url.hostname === '127.0.0.1' &&
      Number(url.port) > 0 &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash &&
      /^\/[a-f0-9]{48}$/u.test(url.pathname)
    );
  } catch {
    return false;
  }
}
export interface WindowsScreenAudioDependencies {
  readonly start: () => Promise<ScreenAudioLease>;
  readonly stop: (leaseId: string) => Promise<void>;
  readonly onEnded: (listener: (leaseId: string) => void) => () => void;
  readonly open?: (url: string, ended: () => void) => Promise<WindowsAudioSource>;
}
const unavailable = () => new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');

/** The helper owns OS process exclusion; renderer accepts only the one consent-bound local stream. */
export async function attachWindowsScreenAudio(
  display: ScreenCapture,
  dependencies: WindowsScreenAudioDependencies,
): Promise<ScreenCapture> {
  let lease: ScreenAudioLease | undefined;
  let audio: WindowsAudioSource | undefined;
  let closed = false;
  let released = false;
  let pendingEnded: string | undefined;
  let unsubscribe: (() => void) | undefined;
  const release = () => {
    if (released || !lease || !/^[a-f0-9]{32}$/u.test(lease.leaseId)) return;
    released = true;
    void dependencies.stop(lease.leaseId).catch(() => {});
  };
  const cleanup = () => {
    if (closed) return;
    closed = true;
    unsubscribe?.();
    display.tracks.forEach((track) => {
      track.off('ended', cleanup);
      track.stop();
    });
    void audio?.close().catch(() => {});
    display.cleanup?.();
    release();
  };
  const ended = () => {
    if (closed) return;
    display.tracks[0]?.emit('ended', display.tracks[0]);
    cleanup();
  };
  display.tracks.forEach((track) => track.on('ended', cleanup));
  try {
    if (
      display.tracks.length !== 1 ||
      display.tracks[0].kind !== 'video' ||
      display.tracks[0].mediaStreamTrack.readyState === 'ended'
    )
      throw unavailable();
    unsubscribe = dependencies.onEnded((id) => {
      if (!lease) pendingEnded = id;
      else if (id === lease.leaseId) ended();
    });
    lease = await dependencies.start();
    if (closed || pendingEnded === lease.leaseId || !windowsAudioLease(lease)) throw unavailable();
    const opened = await (dependencies.open ?? openWindowsAudio)(lease.url, ended);
    if (closed) {
      await opened.close();
      throw unavailable();
    }
    audio = opened;
    return Object.freeze({ tracks: Object.freeze([...display.tracks, audio.track]), cleanup });
  } catch (error) {
    cleanup();
    release();
    throw error;
  }
}

/** Bounded native PCM flows through a private loopback socket, never through Electron IPC. */
export async function openWindowsAudio(url: string, ended: () => void): Promise<WindowsAudioSource> {
  if (!windowsAudioLease({ leaseId: '0'.repeat(32), url })) throw unavailable();
  const context = new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' });
  let node: AudioWorkletNode | undefined;
  let output: MediaStreamTrack | undefined;
  let socket: WebSocket | undefined;
  let closed = false;
  const close = async () => {
    if (closed) return;
    closed = true;
    socket?.close();
    if (node) {
      node.port.onmessage = null;
      node.port.close();
      node.disconnect();
    }
    output?.stop();
    await context.close();
  };
  const fail = () => {
    if (!closed) {
      ended();
      void close().catch(() => {});
    }
  };
  try {
    if (context.sampleRate !== 48000) throw unavailable();
    await context.audioWorklet.addModule(new URL('screen-audio-worklet.js', location.href).href);
    node = new AudioWorkletNode(context, 'gul-screen-audio', {
      numberOfInputs: 0,
      numberOfOutputs: 1,
      outputChannelCount: [2],
    });
    node.port.onmessage = fail;
    node.onprocessorerror = fail;
    const destination = context.createMediaStreamDestination();
    destination.channelCount = 2;
    node.connect(destination);
    output = destination.stream.getAudioTracks()[0];
    if (!output) throw unavailable();
    await context.resume();
    if (context.state !== 'running') throw unavailable();
    socket = new WebSocket(url);
    socket.binaryType = 'arraybuffer';
    socket.onmessage = ({ data }: MessageEvent<unknown>) => {
      if (closed) return;
      if (!(data instanceof ArrayBuffer) || data.byteLength < 24 || data.byteLength > 3856) {
        fail();
        return;
      }
      node!.port.postMessage(data, [data]);
    };
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(unavailable()), 5000);
      const failed = () => {
        clearTimeout(timer);
        reject(unavailable());
        fail();
      };
      socket!.onerror = failed;
      socket!.onclose = failed;
      socket!.onopen = () => {
        clearTimeout(timer);
        socket!.onerror = fail;
        socket!.onclose = fail;
        resolve();
      };
    });
    return {
      track: new LocalAudioTrack(
        output,
        {
          channelCount: 2,
          sampleRate: 48000,
          echoCancellation: false,
          noiseSuppression: false,
          autoGainControl: false,
        },
        true,
        context,
      ),
      close,
    };
  } catch {
    await close().catch(() => {});
    throw unavailable();
  }
}
