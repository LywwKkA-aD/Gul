import { desktopCapturer, dialog, type BrowserWindow, type DesktopCapturerSource } from 'electron';
import type { SessionAuthority } from './session.ts';
import { captureAllowed } from './security.ts';
import { getCaptureCapabilities } from './capture-capabilities.ts';
import { CaptureChooser, type CaptureChoice, type CaptureCapabilities } from './capture-policy.ts';
import { captureRequestFacts, type CaptureStage } from './capture-diagnostics.ts';

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
      const report = (
        stage: CaptureStage,
        extra: Readonly<Record<string, string | boolean | number>> = {},
      ) => {
        try {
          window.webContents.emit(
            'gul-capture-diagnostic',
            Object.freeze({
              stage,
              ...captureRequestFacts(
                request,
                request.frame?.url,
                request.frame === window.webContents.mainFrame,
                epoch !== null && authority.mediaEpoch() === epoch,
              ),
              guardReady: window.webContents.debugger.isAttached(),
              ...extra,
            }),
          );
        } catch {
          /* Observability must not affect a closing frame or its capture decision. */
        }
      };
      const valid = () =>
        epoch !== null &&
        authority.mediaEpoch() === epoch &&
        !window.isDestroyed() &&
        window.webContents.debugger.isAttached() &&
        captureAllowed(request, request.frame?.url, request.frame === window.webContents.mainFrame);
      report('request');
      if (!valid()) {
        report('denied');
        callback(null);
        return;
      }
      void (async () => {
        try {
          const capabilities = await (dependencies.getCapabilities ?? getCaptureCapabilities)();
          report('capabilities', {
            backend: capabilities.backend,
            systemAudio: capabilities.systemAudio,
            audioServer: capabilities.audioServer,
          });
          const selection = await chooser.choose({
            valid,
            audioRequested: request.audioRequested,
            capabilities,
            getSources: async () => {
              const sources = await (
                dependencies.getSources ??
                (() =>
                  desktopCapturer.getSources({
                    types: ['screen', 'window'],
                    thumbnailSize: { width: 0, height: 0 },
                    fetchWindowIcons: false,
                  }))
              )();
              report('sources', {
                count: sources.length,
                screens: sources.filter((source) => source.id.startsWith('screen:')).length,
              });
              return sources;
            },
            pick: async (sources, audio, details) => {
              const choice = await (
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
                })
              )(sources, audio, details);
              const selected = sources[choice.response - 1];
              report('picked', {
                video: selected?.id.startsWith('screen:')
                  ? 'screen'
                  : selected?.id.startsWith('window:')
                    ? 'window'
                    : 'none',
                audio: audio && choice.checkboxChecked,
              });
              return choice;
            },
          });
          report(selection ? 'granted' : 'cancelled', { audio: Boolean(selection?.audio) });
          callback(selection);
        } catch {
          report('failed');
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
