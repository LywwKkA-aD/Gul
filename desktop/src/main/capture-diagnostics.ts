import { APP_ORIGIN } from '../shared/contracts.ts';
import { appPage } from './security.ts';

interface CaptureRequest {
  readonly securityOrigin: string;
  readonly videoRequested: boolean;
  readonly audioRequested: boolean;
  readonly userGesture: boolean;
}

/** Diagnostics contain decisions, never document URLs, source titles or session capabilities. */
export function captureRequestFacts(
  request: CaptureRequest,
  frameURL: string | undefined,
  mainFrame: boolean,
  activeSession: boolean,
) {
  return Object.freeze({
    activeSession,
    mainFrame,
    appFrame: appPage(frameURL),
    appOrigin: request.securityOrigin === APP_ORIGIN,
    videoRequested: request.videoRequested,
    audioRequested: request.audioRequested,
    userGesture: request.userGesture,
  });
}

export type CaptureStage =
  'request' | 'denied' | 'capabilities' | 'sources' | 'picked' | 'granted' | 'cancelled' | 'failed';
