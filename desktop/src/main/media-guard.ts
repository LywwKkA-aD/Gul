import { installDeviceAudioGuard } from '../preload/media-guard.ts';

interface MediaGuardDebugger {
  attach(version: string): void;
  isAttached(): boolean;
  detach(): void;
  sendCommand(method: string, parameters?: Record<string, unknown>): Promise<unknown>;
  on(event: 'detach', listener: () => void): unknown;
  removeListener(event: 'detach', listener: () => void): unknown;
}

/** Private CDP connection: no debugging port, Node in subframes or page IPC is exposed. */
export async function installMediaGuard(
  debuggerAPI: MediaGuardDebugger,
  onDetach: () => void,
): Promise<void> {
  try {
    if (debuggerAPI.isAttached()) throw Error('GUL_MEDIA_GUARD_UNAVAILABLE');
    debuggerAPI.attach('1.3');
    debuggerAPI.on('detach', onDetach);
    await debuggerAPI.sendCommand('Page.enable');
    const registration = await debuggerAPI.sendCommand('Page.addScriptToEvaluateOnNewDocument', {
      // Omitting worldName deliberately runs in every frame's main world, including
      // initial about:blank documents before a parent can borrow their native methods.
      source: `(${installDeviceAudioGuard.toString()})()`,
    });
    if (
      !debuggerAPI.isAttached() ||
      !registration ||
      typeof registration !== 'object' ||
      !('identifier' in registration) ||
      typeof registration.identifier !== 'string' ||
      !registration.identifier
    )
      throw Error('GUL_MEDIA_GUARD_UNAVAILABLE');
  } catch {
    debuggerAPI.removeListener('detach', onDetach);
    if (debuggerAPI.isAttached()) debuggerAPI.detach();
    throw Error('GUL_MEDIA_GUARD_UNAVAILABLE');
  }
}
