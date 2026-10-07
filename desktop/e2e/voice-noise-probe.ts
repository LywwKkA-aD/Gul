/** Test-only RMS taps observe the real Chromium processed input and decoded remote tracks.
 * They never replace microphone samples or weaken the production MediaDevices guards.
 */
export function installVoiceNoiseProbe() {
  type Reading = { time: number; level: number; clips: number };
  type Meter = { kind: 'mic' | 'voice' | 'screen'; track: MediaStreamTrack; values: Reading[]; step: number };
  const meters: Meter[] = [];
  const contexts: AudioContext[] = [];
  let neuralNoise: boolean | undefined;
  let modelRate: number | undefined;
  const voiceContexts = new WeakSet<BaseAudioContext>();
  const processed: MediaStreamTrack[] = [];
  const nativeWorklet = AudioWorkletNode;
  window.AudioWorkletNode = class extends nativeWorklet {
    constructor(context: BaseAudioContext, name: string, options?: AudioWorkletNodeOptions) {
      super(context, name, options);
      if (name !== 'gul-voice') return;
      voiceContexts.add(context);
      this.port.addEventListener('message', ({ data }: MessageEvent<unknown>) => {
        if (!data || typeof data !== 'object') return;
        const value = data as Record<string, unknown>;
        if (
          value.type === 'ready' &&
          typeof value.neuralNoise === 'boolean' &&
          typeof value.sampleRate === 'number'
        ) {
          neuralNoise = value.neuralNoise;
          modelRate = value.sampleRate;
        }
      });
      this.port.start();
    }
  };
  const originalDestination = AudioContext.prototype.createMediaStreamDestination;
  AudioContext.prototype.createMediaStreamDestination = function () {
    const destination = originalDestination.call(this);
    if (voiceContexts.has(this)) processed.push(destination.stream.getAudioTracks()[0]);
    return destination;
  };
  const original = AudioContext.prototype.createMediaStreamSource;
  function meter(source: MediaStreamAudioSourceNode, track: MediaStreamTrack, kind: Meter['kind']) {
    const tap = source.context.createScriptProcessor(2048, 1, 1);
    const silent = source.context.createGain();
    silent.gain.value = 0;
    const values: Reading[] = [];
    tap.onaudioprocess = (event) => {
      const input = event.inputBuffer.getChannelData(0);
      let energy = 0;
      let clips = 0;
      for (const sample of input) {
        energy += sample * sample;
        if (Math.abs(sample) >= 0.99) clips++;
      }
      values.push({ time: performance.now(), level: Math.sqrt(energy / input.length), clips });
      if (values.length > 1200) values.shift();
    };
    source.connect(tap).connect(silent).connect(source.context.destination);
    meters.push({ kind, track, values, step: 2048 / source.context.sampleRate });
  }
  AudioContext.prototype.createMediaStreamSource = function (stream: MediaStream) {
    const source = original.call(this, stream);
    const track = stream.getAudioTracks()[0];
    if (track?.label.includes('Fake') && track.getSettings().deviceId) meter(source, track, 'mic');
    return source;
  };
  Object.defineProperty(window, '__gulVoiceNoiseProbe', {
    value: {
      begin() {
        for (const kind of ['voice', 'screen'] as const) {
          const element = document.querySelector<HTMLAudioElement>(`audio[data-source="${kind}"]`);
          const stream = element?.srcObject as MediaStream | null;
          const track = stream?.getAudioTracks()[0];
          if (!track || meters.some((item) => item.kind === kind && item.track === track)) continue;
          const context = new AudioContext({ sampleRate: 48000 });
          contexts.push(context);
          meter(original.call(context, stream!), track, kind);
          void context.resume();
        }
        return performance.now();
      },
      read(since: number) {
        const current = meters.filter((item) => item.track.readyState === 'live');
        const mic = [...current].reverse().find((item) => item.kind === 'mic');
        const settings = mic?.track.getSettings();
        const levels = (kind: Meter['kind']) =>
          current
            .filter((item) => item.kind === kind)
            .flatMap((item) =>
              item.values.filter((sample) => sample.time >= since).map((sample) => sample.level),
            );
        return {
          mic: levels('mic'),
          voice: levels('voice'),
          screen: levels('screen'),
          clips: current
            .filter((item) => item.kind === 'voice')
            .flatMap((item) => item.values.filter((sample) => sample.time >= since))
            .reduce((sum, sample) => sum + sample.clips, 0),
          voiceStep: current.find((item) => item.kind === 'voice')?.step ?? 2048 / 48000,
          flags: {
            noiseSuppression: settings?.noiseSuppression,
            autoGainControl: settings?.autoGainControl,
            echoCancellation: settings?.echoCancellation,
            channelCount: settings?.channelCount,
            neuralNoise,
            modelRate,
            processedChannels: [...processed]
              .reverse()
              .find((track) => track.readyState === 'live')
              ?.getSettings().channelCount,
          },
          voiceElements: document.querySelectorAll('audio[data-source="voice"]').length,
          silentElements: [
            ...document.querySelectorAll<HTMLAudioElement>('audio[data-source="voice"]'),
          ].every((element) => element.muted && element.volume === 0),
          tracks: current.map((item) => ({
            kind: item.kind,
            enabled: item.track.enabled,
            muted: item.track.muted,
            peak: Math.max(0, ...item.values.map((value) => value.level)),
          })),
        };
      },
      async close() {
        await Promise.all(contexts.map((context) => context.close()));
      },
    },
  });
}

/** Screen audio has independent stereo markers; voice always originates from the speech file. */
export function installVoiceNoiseScreen() {
  navigator.mediaDevices.getDisplayMedia = async () => {
    const canvas = document.createElement('canvas');
    canvas.width = 1280;
    canvas.height = 720;
    const context = canvas.getContext('2d')!;
    let frame = 0;
    const timer = setInterval(() => {
      context.fillStyle = `hsl(${frame++ % 360} 60% 40%)`;
      context.fillRect(0, 0, 1280, 720);
    }, 33);
    const stream = canvas.captureStream(30);
    const audio = new AudioContext({ sampleRate: 48000 });
    const output = audio.createMediaStreamDestination();
    const stereo = audio.createChannelMerger(2);
    for (const [channel, frequency] of [440, 880].entries()) {
      const oscillator = audio.createOscillator();
      const gain = audio.createGain();
      oscillator.frequency.value = frequency;
      gain.gain.value = 0.08;
      oscillator.connect(gain).connect(stereo, 0, channel);
      oscillator.start();
    }
    stereo.connect(output);
    await audio.resume();
    stream.addTrack(output.stream.getAudioTracks()[0]);
    const video = stream.getVideoTracks()[0];
    const stop = video.stop.bind(video);
    video.stop = () => {
      clearInterval(timer);
      stop();
      void audio.close();
    };
    return stream;
  };
}
