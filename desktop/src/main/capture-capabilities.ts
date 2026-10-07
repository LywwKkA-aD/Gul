import { stat } from 'node:fs/promises';
import { release } from 'node:os';
import { join } from 'node:path';
import { captureCapabilities, capturePickerMode, type CaptureCapabilities } from './capture-policy.ts';

/** Report engine support and a local audio-server socket; never expose paths or capture without consent. */
export async function getCaptureCapabilities(
  options: { readonly linuxExcludedAudio?: boolean } = {},
): Promise<CaptureCapabilities> {
  let pulseDetected = false;
  if (process.platform === 'linux') {
    const paths = [
      ...(process.env.PULSE_SERVER?.startsWith('unix:') ? [process.env.PULSE_SERVER.slice(5)] : []),
      ...(process.env.PULSE_RUNTIME_PATH ? [join(process.env.PULSE_RUNTIME_PATH, 'native')] : []),
      ...(process.env.XDG_RUNTIME_DIR ? [join(process.env.XDG_RUNTIME_DIR, 'pulse', 'native')] : []),
      ...(typeof process.getuid === 'function' ? [`/run/user/${process.getuid()}/pulse/native`] : []),
    ];
    pulseDetected = (
      await Promise.all(
        paths.map(async (path) => {
          try {
            return (await stat(path)).isSocket();
          } catch {
            return false;
          }
        }),
      )
    ).some(Boolean);
  }
  return captureCapabilities(
    process.platform,
    release(),
    pulseDetected,
    options.linuxExcludedAudio ?? false,
    capturePickerMode(process.platform, process.env),
  );
}
