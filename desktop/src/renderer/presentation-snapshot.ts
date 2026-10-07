import type { Snapshot } from './media/model.ts';

/** Only the open voice settings panel subscribes to the continuous microphone meter. */
export function presentationSnapshot(read: () => Snapshot): () => Snapshot {
  let previous = read();
  return () => {
    const next = read();
    if (next === previous) return previous;
    if (
      (Object.keys(next) as (keyof Snapshot)[]).some(
        (key) => key !== 'micLevel' && key !== 'voiceActive' && !Object.is(previous[key], next[key]),
      )
    )
      previous = next;
    return previous;
  };
}
