import type { Snapshot } from './model.ts';
import type { MicReading } from './microphone.ts';

type ReadingPatch = Pick<Snapshot, 'micLevel' | 'voiceActive' | 'voiceProcessingAvailable'>;
export function microphoneReading(reading: MicReading, previous: ReadingPatch): ReadingPatch | undefined {
  const micLevel = Math.round(reading.level * 1000) / 1000;
  if (
    previous.micLevel === micLevel &&
    previous.voiceActive === reading.active &&
    previous.voiceProcessingAvailable === reading.available
  )
    return;
  return { micLevel, voiceActive: reading.active, voiceProcessingAvailable: reading.available };
}
