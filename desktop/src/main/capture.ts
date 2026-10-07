import { desktopCapturer, dialog, type BrowserWindow, type DesktopCapturerSource } from 'electron';
import type { SessionAuthority } from './session.ts';
import { captureAllowed } from './security.ts';
import { getCaptureCapabilities } from './capture-capabilities.ts';
import { CaptureChooser, type CaptureChoice, type CaptureCapabilities } from './capture-policy.ts';

export interface CaptureDependencies {
  readonly getSources?: () => Promise<readonly DesktopCapturerSource[]>;
  readonly getCapabilities?: () => Promise<CaptureCapabilities>;
  readonly pick?: (
    sources: readonly DesktopCapturerSource[],
    audio: boolean,
    details: string,
  ) => Promise<CaptureChoice>;
}

/** Capture requires an explicit source choice; a cancelled picker never starts media. */
export function installDisplayCapture(
  window: BrowserWindow,
  authority: SessionAuthority,
  dependencies: CaptureDependencies = {},
): void {
  const chooser = new CaptureChooser();
  window.webContents.session.setDisplayMediaRequestHandler(
    (request, callback) => {
      const epoch = authority.mediaEpoch();
      const valid = () =>
        epoch !== null &&
        authority.mediaEpoch() === epoch &&
        !window.isDestroyed() &&
        captureAllowed(request, request.frame?.url, request.frame === window.webContents.mainFrame);
      if (!valid()) {
        callback(null);
        return;
      }
      void (async () => {
        try {
          const selection = await chooser.choose({
            valid,
            audioRequested: request.audioRequested,
            capabilities: await (dependencies.getCapabilities ?? getCaptureCapabilities)(),
            getSources:
              dependencies.getSources ??
              (() =>
                desktopCapturer.getSources({
                  types: ['screen', 'window'],
                  thumbnailSize: { width: 0, height: 0 },
                  fetchWindowIcons: false,
                })),
            pick:
              dependencies.pick ??
              (async (sources, audio, details) => {
                const selection = await dialog.showMessageBox(window, {
                  type: 'question',
                  title: 'Демонстрация экрана',
                  message: audio
                    ? 'Выберите экран или окно: передаются изображение и звук компьютера'
                    : 'Выберите экран или окно для демонстрации',
                  detail: audio
                    ? details
                    : 'Передаётся только изображение: системный звук недоступен для этого источника или ОС. ' +
                      details,
                  buttons: ['Отмена', ...sources.map((source) => source.name)],
                  cancelId: 0,
                  defaultId: 0,
                  noLink: true,
                });
                return { response: selection.response, checkboxChecked: audio };
              }),
          });
          callback(selection);
        } catch {
          try {
            callback(null);
          } catch {
            /* The requesting frame may have closed. */
          }
        }
      })();
    },
    { useSystemPicker: false },
  );
}
