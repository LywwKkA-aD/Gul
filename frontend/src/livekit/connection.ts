import { ConnectionError, ConnectionErrorReason } from 'livekit-client';

export type RuntimeIssue = 'SCREEN_CONTEXT' | 'SCREEN_RTC_UNAVAILABLE';
export type ConnectionStage = 'grant' | 'runtime' | 'connect';
type FailureCode = RuntimeIssue | 'SCREEN_GRANT_REQUEST' | 'SCREEN_GRANT_INVALID' |
  'SCREEN_RUNTIME' | 'SCREEN_TIMEOUT' | 'SCREEN_SIGNAL' | 'SCREEN_AUTH' |
  'SCREEN_CANCELLED' | 'SCREEN_CONNECT';
interface RuntimeScope {
  isSecureContext?: boolean;
  RTCPeerConnection?: { prototype?: { addTransceiver?: unknown; addTrack?: unknown } };
}

/** A capability check must not open media devices, sockets or issue credentials. */
export function runtimeIssue(scope: RuntimeScope = globalThis): RuntimeIssue | undefined {
  if (scope.isSecureContext !== true) return 'SCREEN_CONTEXT';
  const prototype = scope.RTCPeerConnection?.prototype;
  if (!prototype || (typeof prototype.addTransceiver !== 'function' && typeof prototype.addTrack !== 'function')) {
    return 'SCREEN_RTC_UNAVAILABLE';
  }
  return undefined;
}

export class ScreenGrantError extends Error {
  readonly code: 'SCREEN_GRANT_REQUEST' | 'SCREEN_GRANT_INVALID';
  constructor(code: ScreenGrantError['code']) {
    super('Screen session is no longer available');
    this.code = code;
  }
}

/** Never return SDK messages, URLs, context objects, tokens or arbitrary codes. */
export function connectionFailure(stage: ConnectionStage, error: unknown): { code: FailureCode; retryable: boolean } {
  if (stage === 'grant') {
    const code = error instanceof ScreenGrantError ? error.code : 'SCREEN_GRANT_REQUEST';
    return { code, retryable: code === 'SCREEN_GRANT_REQUEST' };
  }
  if (stage === 'runtime') return { code: 'SCREEN_RUNTIME', retryable: false };
  if (error instanceof ConnectionError) {
    switch (error.reason) {
      case ConnectionErrorReason.Timeout: return { code: 'SCREEN_TIMEOUT', retryable: true };
      case ConnectionErrorReason.WebSocket:
      case ConnectionErrorReason.ServerUnreachable:
      case ConnectionErrorReason.ServiceNotFound: return { code: 'SCREEN_SIGNAL', retryable: true };
      case ConnectionErrorReason.NotAllowed: return { code: 'SCREEN_AUTH', retryable: false };
      case ConnectionErrorReason.Cancelled:
      case ConnectionErrorReason.LeaveRequest: return { code: 'SCREEN_CANCELLED', retryable: false };
    }
  }
  return { code: 'SCREEN_CONNECT', retryable: true };
}

export function connectionMessage(code: FailureCode): string {
  if (code === 'SCREEN_RTC_UNAVAILABLE') {
    return 'Демонстрации недоступны: встроенный браузер не поддерживает WebRTC. Голос продолжает работать. (SCREEN_RTC_UNAVAILABLE)';
  }
  if (code === 'SCREEN_CONTEXT') {
    return 'Демонстрации недоступны: встроенный браузер не разрешил безопасный доступ к медиа. (SCREEN_CONTEXT)';
  }
  return `Не удалось подключить демонстрации к каналу. (${code})`;
}

export function waitForRetry(delay: number, signal: AbortSignal): Promise<boolean> {
  if (signal.aborted) return Promise.resolve(false);
  return new Promise((resolve) => {
    const done = (elapsed: boolean) => {
      clearTimeout(timer);
      signal.removeEventListener('abort', cancel);
      resolve(elapsed);
    };
    const cancel = () => done(false);
    const timer = setTimeout(() => done(true), delay);
    signal.addEventListener('abort', cancel, { once: true });
  });
}
