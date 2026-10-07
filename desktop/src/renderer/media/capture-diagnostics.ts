const names = new Set([
  'NotAllowedError',
  'NotFoundError',
  'NotReadableError',
  'OverconstrainedError',
  'AbortError',
  'InvalidStateError',
  'NotSupportedError',
  'SecurityError',
  'TypeMismatchError',
]);

/** Chromium errors can include private paths or device names in their message. */
export function captureFailureName(error: unknown): string {
  return error instanceof DOMException && names.has(error.name) ? error.name : 'UnknownError';
}
