import { desktopCapturer, dialog, type BrowserWindow } from 'electron';
import type { SessionAuthority } from './session.ts';
import { captureAllowed, captureAudio } from './security.ts';

/** Capture requires an explicit source choice; a cancelled picker never starts media. */
export function installDisplayCapture(window: BrowserWindow, authority: SessionAuthority): void {
  let choosing = false;
  window.webContents.session.setDisplayMediaRequestHandler(
    (request, callback) => {
      const epoch = authority.mediaEpoch();
      const valid = () =>
        epoch !== null &&
        authority.mediaEpoch() === epoch &&
        !window.isDestroyed() &&
        captureAllowed(request, request.frame?.url, request.frame === window.webContents.mainFrame);
      if (choosing || !valid()) {
        callback(null);
        return;
      }
      choosing = true;
      void (async () => {
        try {
          const sources = await desktopCapturer.getSources({
            types: ['screen', 'window'],
            thumbnailSize: { width: 0, height: 0 },
            fetchWindowIcons: false,
          });
          if (!valid() || !sources.length) {
            callback(null);
            return;
          }
          const supportsAudio = process.platform === 'win32' && request.audioRequested;
          const choice = await dialog.showMessageBox(window, {
            type: 'question',
            title: 'Демонстрация экрана',
            message: 'Выберите экран или окно для демонстрации',
            detail: supportsAudio
              ? 'Системный звук включает все воспроизводимые приложения, включая голос собеседников. Используйте наушники.'
              : 'Этот режим передаёт изображение. Захват системного звука для этой ОС пока не подключён.',
            buttons: ['Отмена', ...sources.map((source) => source.name)],
            cancelId: 0,
            defaultId: 0,
            noLink: true,
            ...(supportsAudio ? { checkboxLabel: 'Передавать системный звук', checkboxChecked: false } : {}),
          });
          if (!valid() || choice.response < 1 || choice.response > sources.length) {
            callback(null);
            return;
          }
          const audio = captureAudio(process.platform, request.audioRequested, choice.checkboxChecked);
          callback({ video: sources[choice.response - 1], ...(audio ? { audio } : {}) });
        } catch {
          try {
            callback(null);
          } catch {
            /* The requesting frame may have closed. */
          }
        } finally {
          choosing = false;
        }
      })();
    },
    { useSystemPicker: false },
  );
}
