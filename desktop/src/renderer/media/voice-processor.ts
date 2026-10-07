import { Track, type LocalAudioTrack, type AudioProcessorOptions, type TrackProcessor } from 'livekit-client';
import { voiceSettings, type VoiceReading, type VoiceSettings } from './voice-gate.ts';

export interface VoiceProcessorHandle {
  readonly update: (settings: VoiceSettings) => void;
  readonly destroy: () => Promise<void>;
  readonly setMuted?: (muted: boolean) => void;
}
interface ProcessorDependencies {
  readonly node?: (context: AudioContext) => AudioWorkletNode;
  readonly stream?: (track: MediaStreamTrack) => MediaStream;
  readonly moduleURL?: string;
}
const loaded = new WeakMap<AudioContext, Promise<void>>();

/** One mono worklet applies gain and VAD; LiveKit owns the shared AudioContext and raw microphone. */
export class VoiceProcessor
  implements TrackProcessor<Track.Kind.Audio, AudioProcessorOptions>, VoiceProcessorHandle
{
  readonly name = 'gul-voice';
  processedTrack?: MediaStreamTrack;
  private source?: MediaStreamAudioSourceNode;
  private node?: AudioWorkletNode;
  private generation = 0;
  private settings: VoiceSettings;
  private context?: AudioContext;
  private muted = true;
  private readonly reading: (reading: Pick<VoiceReading, 'level' | 'active'>) => void;
  private readonly dependencies: ProcessorDependencies;
  constructor(
    settings: VoiceSettings,
    reading: (reading: Pick<VoiceReading, 'level' | 'active'>) => void,
    dependencies: ProcessorDependencies = {},
  ) {
    this.settings = settings;
    this.reading = reading;
    this.dependencies = dependencies;
  }
  async init(options: AudioProcessorOptions): Promise<void> {
    const generation = ++this.generation;
    this.releaseGraph();
    // LiveKit 2.22.3 device restarts pass the raw track but omit the prior shared context.
    const context = options.audioContext ?? this.context;
    if (!context?.audioWorklet) throw new Error('Обработка микрофона недоступна.');
    this.context = context;
    let module = loaded.get(context);
    if (!module) {
      const url = this.dependencies.moduleURL ?? new URL('voice-worklet.js', location.href).href;
      module = context.audioWorklet.addModule(url).catch(() => {
        loaded.delete(context);
        throw new Error('Не удалось запустить обработку микрофона.');
      });
      loaded.set(context, module);
    }
    await module;
    if (this.generation !== generation) throw new Error('Обработка микрофона отменена.');
    try {
      const source = context.createMediaStreamSource(
        (this.dependencies.stream ?? ((track) => new MediaStream([track])))(options.track),
      );
      this.source = source;
      const destination = context.createMediaStreamDestination();
      const output = destination.stream.getAudioTracks()[0];
      if (!output) throw new Error();
      output.enabled = !this.muted && options.track.enabled;
      this.processedTrack = output;
      const node = (
        this.dependencies.node ??
        ((audioContext) =>
          new AudioWorkletNode(audioContext, this.name, {
            numberOfInputs: 1,
            numberOfOutputs: 1,
            outputChannelCount: [1],
            processorOptions: this.settings,
          }))
      )(context);
      this.node = node;
      node.port.onmessage = ({ data }: MessageEvent<unknown>) => {
        if (this.generation !== generation || !data || typeof data !== 'object') return;
        const value = data as Record<string, unknown>;
        if (
          value.type === 'level' &&
          typeof value.level === 'number' &&
          Number.isFinite(value.level) &&
          value.level >= 0 &&
          value.level <= 1 &&
          typeof value.active === 'boolean'
        )
          this.reading({ level: value.level, active: value.active });
      };
      source.connect(node);
      node.connect(destination);
    } catch {
      await this.destroy();
      throw new Error('Не удалось запустить обработку микрофона.');
    }
  }
  async restart(options: AudioProcessorOptions): Promise<void> {
    await this.init(options);
  }
  update = (settings: VoiceSettings): void => {
    this.settings = voiceSettings(this.settings, settings);
    this.node?.port.postMessage({ type: 'settings', settings: this.settings });
  };
  setMuted = (muted: boolean): void => {
    this.muted = muted;
    if (this.processedTrack) this.processedTrack.enabled = !muted;
  };
  destroy = async (): Promise<void> => {
    this.generation++;
    this.releaseGraph();
  };
  private releaseGraph(): void {
    const node = this.node;
    const source = this.source;
    const output = this.processedTrack;
    this.node = undefined;
    this.source = undefined;
    this.processedTrack = undefined;
    if (node) {
      node.port.onmessage = null;
      node.port.close();
      node.disconnect();
    }
    source?.disconnect();
    output?.stop();
  }
}

export async function attachVoiceProcessor(
  track: LocalAudioTrack,
  settings: VoiceSettings,
  reading: (reading: Pick<VoiceReading, 'level' | 'active'>) => void,
): Promise<VoiceProcessorHandle | undefined> {
  if (typeof AudioWorkletNode === 'undefined' || typeof track.setProcessor !== 'function') return;
  const processor = new VoiceProcessor(settings, reading);
  try {
    await track.setProcessor(processor);
    return processor;
  } catch {
    await processor.destroy();
    throw new Error('Не удалось запустить обработку микрофона.');
  }
}
