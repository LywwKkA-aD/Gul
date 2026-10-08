import type { VoiceSettings } from './media/voice-gate.ts';

export type RangePatch = Partial<Pick<VoiceSettings, 'inputGain' | 'thresholdDb' | 'holdMs'>>;
interface Batch {
  readonly patch: RangePatch;
  readonly resolve: readonly (() => void)[];
  readonly reject: readonly ((failure: unknown) => void)[];
}
/** Keep one operation in flight and combine newer continuous values without dropping intent. */
export class RangeUpdates {
  private active = false;
  private pending?: Batch;
  run(patch: RangePatch, apply: (patch: RangePatch) => Promise<void>): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      this.pending = {
        patch: Object.freeze({ ...this.pending?.patch, ...patch }),
        resolve: [...(this.pending?.resolve ?? []), resolve],
        reject: [...(this.pending?.reject ?? []), reject],
      };
      if (!this.active) void this.flush(apply);
    });
  }
  private async flush(apply: (patch: RangePatch) => Promise<void>): Promise<void> {
    this.active = true;
    while (this.pending) {
      const batch = this.pending;
      this.pending = undefined;
      try {
        await apply(batch.patch);
        batch.resolve.forEach((resolve) => resolve());
      } catch (failure) {
        batch.reject.forEach((reject) => reject(failure));
      }
    }
    this.active = false;
  }
}
