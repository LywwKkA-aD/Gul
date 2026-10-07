import type { LocalAudioTrack } from 'livekit-client';
import type { ScreenAudioLease } from '../../shared/contracts.ts';
import type { ScreenCapture } from './model.ts';

export interface LinuxScreenAudioDependencies {
  readonly start: () => Promise<ScreenAudioLease>;
  readonly stop: (leaseId: string) => Promise<void>;
  readonly findDevice: (label: string) => Promise<string | null>;
  readonly capture: (deviceId: string) => Promise<LocalAudioTrack>;
  readonly onEnded: (listener: (leaseId: string) => void) => () => void;
}
const unavailable = () => new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');

/** The only accepted input is the private virtual source created after owned display consent. */
export async function attachLinuxScreenAudio(
  display: ScreenCapture,
  dependencies: LinuxScreenAudioDependencies,
): Promise<ScreenCapture> {
  let lease: ScreenAudioLease | undefined;
  let audio: LocalAudioTrack | undefined;
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
    audio?.stop();
    display.cleanup?.();
    release();
  };
  display.tracks.forEach((track) => track.on('ended', cleanup));
  try {
    if (display.tracks.some((track) => track.mediaStreamTrack.readyState === 'ended')) throw unavailable();
    unsubscribe = dependencies.onEnded((leaseId) => {
      if (!lease) {
        pendingEnded = leaseId;
        return;
      }
      if (leaseId !== lease.leaseId || closed) return;
      // LocalTrack.stop() does not emit ended. Notify the controller so publication stops too.
      display.tracks[0]?.emit('ended', display.tracks[0]);
      cleanup();
    });
    lease = await dependencies.start();
    if (closed || pendingEnded === lease.leaseId) {
      release();
      throw unavailable();
    }
    if (!/^[a-f0-9]{32}$/u.test(lease.leaseId) || lease.deviceLabel !== `Gul-Screen-Audio-${lease.leaseId}`)
      throw unavailable();
    const deviceId = await dependencies.findDevice(lease.deviceLabel);
    if (!deviceId || closed) throw unavailable();
    const captured = await dependencies.capture(deviceId);
    if (closed) {
      captured.stop();
      throw unavailable();
    }
    audio = captured;
    return Object.freeze({ tracks: Object.freeze([...display.tracks, audio]), cleanup });
  } catch (error) {
    cleanup();
    release();
    throw error;
  }
}

/** Missing labels never select a default input, and device enumeration has a bounded deadline. */
export async function findPrivateAudioDevice(
  label: string,
  mediaDevices: Pick<MediaDevices, 'enumerateDevices'> = navigator.mediaDevices,
  timeoutMs = 3000,
): Promise<string | null> {
  const deadline = Date.now() + timeoutMs;
  do {
    const remaining = deadline - Date.now();
    if (remaining <= 0) return null;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let devices: MediaDeviceInfo[] | null;
    try {
      devices = await Promise.race([
        mediaDevices.enumerateDevices(),
        new Promise<null>((resolve) => {
          timer = setTimeout(() => resolve(null), remaining);
        }),
      ]);
    } finally {
      clearTimeout(timer);
    }
    if (!devices) return null;
    const matches = devices.filter(
      (device) =>
        device.kind === 'audioinput' &&
        device.label === label &&
        device.deviceId &&
        !['default', 'communications'].includes(device.deviceId),
    );
    if (matches.length === 1) return matches[0].deviceId;
    if (matches.length > 1) return null;
    await new Promise((resolve) => setTimeout(resolve, Math.max(0, Math.min(50, deadline - Date.now()))));
  } while (Date.now() < deadline);
  return null;
}
