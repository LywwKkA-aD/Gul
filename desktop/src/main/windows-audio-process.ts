import { spawn, type SpawnOptions } from 'node:child_process';
import { access, lstat } from 'node:fs/promises';
import { constants } from 'node:fs';
import { isAbsolute } from 'node:path';
import type { AudioHelperProcess } from './screen-audio.ts';

export interface WindowsAudioProcessOptions {
  readonly executable: string;
  readonly checkExecutable?: () => Promise<boolean>;
  readonly spawn?: (file: string, args: readonly string[], options: SpawnOptions) => AudioHelperProcess;
  readonly stopTimeoutMs?: number;
  readonly killGraceTimeoutMs?: number;
}
export async function executableAvailable(options: WindowsAudioProcessOptions): Promise<boolean> {
  if (!isAbsolute(options.executable)) return false;
  try {
    if (options.checkExecutable) return await options.checkExecutable();
    const file = await lstat(options.executable);
    if (!file.isFile() || file.isSymbolicLink()) return false;
    await access(options.executable, constants.F_OK);
    return true;
  } catch {
    return false;
  }
}
export function launchWindowsAudio(
  options: WindowsAudioProcessOptions,
  command: '--probe' | '--capture',
): AudioHelperProcess {
  const settings: SpawnOptions = {
    stdio: ['pipe', 'pipe', 'ignore'],
    shell: false,
    windowsHide: true,
    env: Object.fromEntries(
      ['SYSTEMROOT', 'SystemRoot', 'WINDIR', 'TEMP', 'TMP', 'LOCALAPPDATA'].flatMap((name) =>
        process.env[name] ? [[name, process.env[name]]] : [],
      ),
    ),
  };
  if (options.spawn) return options.spawn(options.executable, [command], settings);
  const child = spawn(options.executable, [command], settings);
  if (!child.stdin || !child.stdout) {
    child.kill();
    throw new Error('GUL_SCREEN_AUDIO_UNAVAILABLE');
  }
  return child as AudioHelperProcess;
}
export async function stopWindowsAudio(
  child: AudioHelperProcess,
  options: WindowsAudioProcessOptions,
): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolve) => {
    let complete = false;
    let grace: ReturnType<typeof setTimeout> | undefined;
    const finish = () => {
      if (complete) return;
      complete = true;
      clearTimeout(timer);
      clearTimeout(grace);
      child.off('exit', finish);
      resolve();
    };
    const timer = setTimeout(() => {
      grace = setTimeout(finish, options.killGraceTimeoutMs ?? 1000);
      try {
        child.kill('SIGKILL');
      } catch {
        /* Exit is still observed before the bounded grace expires. */
      }
    }, options.stopTimeoutMs ?? 2000);
    child.once('exit', finish);
    try {
      child.stdin.end();
    } catch {
      /* Forced termination follows if EOF could not be delivered. */
    }
  });
}
