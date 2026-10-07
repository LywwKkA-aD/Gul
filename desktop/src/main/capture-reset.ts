/** Revoke display consent now; start the broker's epoch fence without waiting for native cleanup. */
export function runWithCaptureReset<T>(reset: () => Promise<void>, action: () => Promise<T>): Promise<T> {
  let closing: Promise<void>;
  try {
    closing = Promise.resolve(reset());
  } catch (error) {
    return Promise.reject(error);
  }
  let operation: Promise<T>;
  try {
    operation = Promise.resolve(action());
  } catch (error) {
    operation = Promise.reject(error);
  }
  return Promise.all([operation, closing]).then(([result]) => result);
}
