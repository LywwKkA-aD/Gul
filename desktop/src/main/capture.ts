import { desktopCapturer, type BrowserWindow, type DesktopCapturerSource } from 'electron';
import type { SessionAuthority } from './session.ts';
import { captureAllowed } from './security.ts';
import { getCaptureCapabilities } from './capture-capabilities.ts';
import {
  CaptureChooser,
  capturePickerMode,
  type CaptureChoice,
  type CaptureCapabilities,
} from './capture-policy.ts';
import { DisplayCaptureConsent } from './capture-consent.ts';
import { captureRequestFacts, type CaptureStage } from './capture-diagnostics.ts';

export interface CaptureDependencies {
  readonly getSources?: () => Promise<readonly DesktopCapturerSource[]>;
  readonly getCapabilities?: () => Promise<CaptureCapabilities>;
  readonly pick?: (
    sources: readonly DesktopCapturerSource[],
    audio: boolean,
    details: string,
    valid: () => boolean,
  ) => Promise<CaptureChoice>;
}

/** Capture requires an explicit source choice; a cancelled picker never starts media. */
export function installDisplayCapture(
  window: BrowserWindow,
  authority: SessionAuthority,
  dependencies: CaptureDependencies = {},
): DisplayCaptureConsent {
  const chooser = new CaptureChooser();
  const consent = new DisplayCaptureConsent();
  window.webContents.session.setDisplayMediaRequestHandler(
    (request, callback) => {
      const requestCurrent = consent.beginRequest();
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
        requestCurrent() &&
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
          const portalSelection = capturePickerMode(process.platform, process.env) === 'portal';
          const selection = await chooser.choose({
            valid,
            portalSelection,
            loopbackAudio: !['linux', 'win32'].includes(process.platform),
            audioRequested: request.audioRequested,
            capabilities,
            getSources: async () => {
              const sources = await (
                dependencies.getSources ??
                (() =>
                  desktopCapturer.getSources({
                    types: ['screen', 'window'],
                    thumbnailSize: { width: 320, height: 180 },
                    fetchWindowIcons: false,
                  }))
              )();
              const selectable = portalSelection
                ? sources
                : sources.filter((source) => source.id !== window.getMediaSourceId());
              report('sources', {
                count: selectable.length,
                screens: selectable.filter((source) => source.id.startsWith('screen:')).length,
              });
              return selectable;
            },
            pick: async (sources, audio, details) => {
              const choice = await (
                dependencies.pick ?? (async () => ({ response: 0, checkboxChecked: false }))
              )(sources, audio, details, valid);
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
          if (selection) consent.accept(valid, request.audioRequested, capabilities.systemAudio);
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
  return consent;
}
