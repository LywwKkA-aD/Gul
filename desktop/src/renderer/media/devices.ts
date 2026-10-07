import type { Room } from 'livekit-client';

type AudioDevice = 'audioinput' | 'audiooutput';

/** Preferences survive room changes; selecting before join never starts hardware capture. */
export class Devices {
  private input?: string;
  private output?: string;

  get microphoneDevice(): string | undefined {
    return this.input;
  }

  async set(
    kind: AudioDevice,
    id: string,
    voice: Room | undefined,
    screen: () => Room | undefined,
    current: () => boolean,
  ): Promise<void> {
    if (!id) return;
    const previous = kind === 'audioinput' ? this.input : this.output;
    if (previous === id) return;
    if (!voice) {
      this.save(kind, id);
      return;
    }
    let switched = false;
    let screenRoom: Room | undefined;
    try {
      if ((await voice.switchActiveDevice(kind, id)) === false) throw new Error();
      switched = true;
      if (!current()) throw new Error();
      if (kind === 'audiooutput') {
        screenRoom = screen();
        if (screenRoom && (await screenRoom.switchActiveDevice(kind, id)) === false) throw new Error();
      }
      if (!current()) throw new Error();
      this.save(kind, id);
    } catch (error) {
      // A failed screen-device change must not leave voice on an unconfirmed output.
      if (kind === 'audiooutput' && switched && current()) {
        await Promise.all(
          [voice, screenRoom].map(async (room) => {
            try {
              await room?.switchActiveDevice(kind, previous ?? 'default');
            } catch {
              /* Keep the original failure. */
            }
          }),
        );
      }
      throw error;
    }
  }

  async applyOutput(room: Room): Promise<void> {
    if (this.output && (await room.switchActiveDevice('audiooutput', this.output)) === false)
      throw new Error();
  }

  private save(kind: AudioDevice, id: string): void {
    if (kind === 'audioinput') this.input = id;
    else this.output = id;
  }
}
