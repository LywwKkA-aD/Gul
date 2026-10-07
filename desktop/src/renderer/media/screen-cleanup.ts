/** Unpublish and disconnect belong to one channel epoch even when different UI actions close it.
 * A new channel does not wait for stale cleanup; the same channel waits for every owned operation.
 */
export class ScreenCleanup {
  private readonly pending = new Map<number, Set<Promise<void>>>();

  add(epoch: number, operation: Promise<void>): Promise<void> {
    const work = this.pending.get(epoch) ?? new Set<Promise<void>>();
    this.pending.set(epoch, work);
    const tracked = operation.finally(() => {
      work.delete(tracked);
      if (!work.size) this.pending.delete(epoch);
    });
    work.add(tracked);
    return tracked;
  }

  async wait(epoch: number): Promise<void> {
    while (this.pending.get(epoch)?.size) await Promise.all([...this.pending.get(epoch)!]);
  }
}
