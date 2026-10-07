import { randomBytes } from 'node:crypto';
import type { CaptureChoice } from './capture-policy.ts';
import type { CapturePickerRequest, CapturePickerSource } from '../shared/capture-picker.ts';

interface Source {
  readonly id: string;
  readonly name: string;
  readonly thumbnail?: { toDataURL(): string };
}
interface Options {
  readonly push: (request: CapturePickerRequest | null) => void;
  readonly nonce?: () => string;
}
interface Pending {
  readonly requestId: string;
  readonly indices: ReadonlyMap<string, number>;
  readonly audio: boolean;
  readonly valid: () => boolean;
  readonly resolve: (choice: CaptureChoice) => void;
  readonly check: ReturnType<typeof setInterval>;
  readonly deadline: ReturnType<typeof setTimeout>;
}
const cancelled = (): CaptureChoice => ({ response: 0, checkboxChecked: false });
const validNonce = (value: unknown): value is string =>
  typeof value === 'string' && /^[a-f0-9]{32}$/u.test(value);
const current = (valid: () => boolean): boolean => {
  try {
    return valid() === true;
  } catch {
    return false;
  }
};
const text = (value: string, limit: number): string =>
  value
    .replace(/[\u0000-\u001f\u007f]/gu, ' ')
    .trim()
    .slice(0, limit);
const preview = (source: Source): string | null => {
  try {
    const image = source.thumbnail?.toDataURL();
    return image &&
      image.length <= 1024 * 1024 &&
      /^data:image\/png;base64,iVBORw0KGgo[A-Za-z0-9+/]*={0,2}$/u.test(image)
      ? image
      : null;
  } catch {
    return null;
  }
};

/** Only an opaque card key for the current epoch may resolve a native display request. */
export class CapturePicker {
  private readonly options: Options;
  private pending?: Pending;
  constructor(options: Options) {
    this.options = options;
  }
  async choose(
    sources: readonly Source[],
    audio: boolean,
    details: string,
    valid: () => boolean,
  ): Promise<CaptureChoice> {
    if (this.pending && !current(this.pending.valid)) this.cancel();
    if (this.pending || !current(valid)) return cancelled();
    try {
      const nonce = this.options.nonce ?? (() => randomBytes(16).toString('hex'));
      const requestId = nonce();
      if (!validNonce(requestId)) return cancelled();
      const keys = new Set([requestId]);
      const indices = new Map<string, number>();
      const cards: CapturePickerSource[] = [];
      let budget = 12 * 1024 * 1024;
      for (let index = 0; index < sources.length && cards.length < 120; index++) {
        const source = sources[index];
        const kind = source.id.startsWith('screen:')
          ? 'screen'
          : source.id.startsWith('window:')
            ? 'window'
            : null;
        if (!kind) continue;
        const sourceKey = nonce();
        if (!validNonce(sourceKey) || keys.has(sourceKey)) return cancelled();
        keys.add(sourceKey);
        indices.set(sourceKey, index + 1);
        const image = preview(source);
        const thumbnail = image && image.length <= budget ? image : null;
        if (thumbnail) budget -= thumbnail.length;
        cards.push(
          Object.freeze({
            sourceKey,
            kind,
            name: text(source.name, 240) || `${kind === 'screen' ? 'Экран' : 'Окно'} ${cards.length + 1}`,
            thumbnail,
          }),
        );
      }
      if (!cards.length || !current(valid)) return cancelled();
      const request = Object.freeze({
        requestId,
        sources: Object.freeze(cards),
        audio,
        details: text(details, 2000),
      });
      return await new Promise<CaptureChoice>((resolve) => {
        const check = setInterval(() => {
          if (this.pending?.requestId === requestId && !current(valid)) this.cancel();
        }, 100);
        const deadline = setTimeout(() => {
          if (this.pending?.requestId === requestId) this.cancel();
        }, 120_000);
        check.unref?.();
        deadline.unref?.();
        this.pending = { requestId, indices, audio, valid, resolve, check, deadline };
        try {
          this.options.push(request);
        } catch {
          this.cancel();
        }
      });
    } catch {
      return cancelled();
    }
  }
  select(requestId: unknown, sourceKey: unknown): boolean {
    const pending = this.pending;
    if (!pending || !validNonce(requestId) || pending.requestId !== requestId) return false;
    if (!current(pending.valid)) {
      this.cancel();
      return false;
    }
    if (sourceKey === null) {
      this.cancel();
      return true;
    }
    if (!validNonce(sourceKey)) return false;
    const response = pending.indices.get(sourceKey);
    if (!response) return false;
    this.finish({ response, checkboxChecked: pending.audio });
    return true;
  }
  cancel(): void {
    this.finish(cancelled());
  }
  private finish(choice: CaptureChoice): void {
    const pending = this.pending;
    if (!pending) return;
    this.pending = undefined;
    clearInterval(pending.check);
    clearTimeout(pending.deadline);
    try {
      this.options.push(null);
    } catch {
      /* A closed frame must not retain a native request or expose raw errors. */
    }
    pending.resolve(Object.freeze(choice));
  }
}
