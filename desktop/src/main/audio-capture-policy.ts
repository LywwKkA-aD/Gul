import type { CommandLine } from 'electron';

const inputVolumeAdjustment = 'WebRtcAllowInputVolumeAdjustment';

/** Prevent WebRTC from changing system microphone volume.
 * Chromium 152.0.7977.130 media/webrtc/helpers.cc:69-91 keeps AGC2 digital enabled.
 * https://github.com/chromium/chromium/blob/152.0.7977.130/media/webrtc/helpers.cc#L69-L91
 */
export function installAudioCapturePolicy(
  commandLine: Pick<CommandLine, 'getSwitchValue' | 'appendSwitch'>,
  platform: NodeJS.Platform,
): void {
  if (!['darwin', 'linux', 'win32'].includes(platform)) return;
  const disabled = commandLine.getSwitchValue('disable-features');
  if (disabled.split(',').some((feature) => feature.trim() === inputVolumeAdjustment)) return;
  commandLine.appendSwitch(
    'disable-features',
    disabled ? `${disabled},${inputVolumeAdjustment}` : inputVolumeAdjustment,
  );
}
