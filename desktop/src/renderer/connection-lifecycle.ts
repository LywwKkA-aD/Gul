export type ConnectionStep<T> = { readonly accepted: true; readonly value: T } | { readonly accepted: false };
export interface ConnectionCleanup {
  readonly current: boolean;
  readonly failure?: unknown;
}
type Action<T> = () => Promise<T>;

/** Cancellation fences every awaited connection step and dispatches broker teardown immediately. */
export class ConnectionLifecycle {
  private revision = 0;
  private running?: number;
  private readonly pending = new Set<Promise<ConnectionCleanup>>();
  begin(): number | null {
    if (this.running !== undefined || this.pending.size) return null;
    const operation = ++this.revision;
    this.running = operation;
    return operation;
  }
  invalidate(): number {
    this.running = undefined;
    return ++this.revision;
  }
  current(operation: number): boolean {
    return this.revision === operation;
  }
  finish(operation: number): boolean {
    if (!this.current(operation)) return false;
    this.running = undefined;
    return true;
  }
  async step<T>(operation: number, action: Action<T>): Promise<ConnectionStep<T>> {
    if (!this.current(operation)) return { accepted: false };
    try {
      const value = await action();
      return this.current(operation) ? { accepted: true, value } : { accepted: false };
    } catch (failure) {
      if (!this.current(operation)) return { accepted: false };
      throw failure;
    }
  }
  cleanup(
    operation: number,
    leaveMedia: Action<unknown>,
    disconnectBroker: Action<unknown>,
  ): Promise<ConnectionCleanup> {
    if (!this.current(operation)) return Promise.resolve({ current: false });
    let complete!: (result: ConnectionCleanup) => void;
    const pending = new Promise<ConnectionCleanup>((resolve) => {
      complete = resolve;
    });
    this.pending.add(pending);
    // Invoke both now; an old media disconnect must never postpone a broker cancellation.
    const tasks = [this.invoke(disconnectBroker), this.invoke(leaveMedia)];
    void Promise.allSettled(tasks).then((results) => {
      this.pending.delete(pending);
      const current = this.current(operation);
      const failure = results.find((result) => result.status === 'rejected');
      complete(current && failure ? { current, failure: failure.reason } : { current });
    });
    return pending;
  }
  private invoke(action: Action<unknown>): Promise<unknown> {
    try {
      return Promise.resolve(action());
    } catch (failure) {
      return Promise.reject(failure);
    }
  }
}
