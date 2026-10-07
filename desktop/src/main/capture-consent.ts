export interface AudioCaptureConsent {
  readonly valid: () => boolean;
}
const safe = (valid: () => boolean) => {
  try {
    return valid();
  } catch {
    return false;
  }
};

/** A display picker grants one local audio lease; consent never crosses IPC as a source ID. */
export class DisplayCaptureConsent {
  private generation = 0;
  private request = 0;
  private accepted?: { readonly valid: () => boolean; readonly at: number; readonly generation: number };
  private readonly now: () => number;
  constructor(now: () => number = Date.now) {
    this.now = now;
  }
  invalidate(): void {
    ++this.generation;
    ++this.request;
    this.accepted = undefined;
  }
  beginRequest(): () => boolean {
    this.invalidate();
    const request = this.request;
    return () => request === this.request;
  }
  accept(valid: () => boolean, audioRequested = false, audioAvailable = false): void {
    ++this.generation;
    this.accepted = undefined;
    if (audioRequested && audioAvailable && safe(valid))
      this.accepted = { valid, at: this.now(), generation: this.generation };
  }
  claimAudio(): AudioCaptureConsent | null {
    const accepted = this.accepted;
    this.accepted = undefined;
    if (!accepted || this.now() < accepted.at || this.now() - accepted.at > 15000 || !safe(accepted.valid))
      return null;
    return Object.freeze({ valid: () => accepted.generation === this.generation && safe(accepted.valid) });
  }
}
