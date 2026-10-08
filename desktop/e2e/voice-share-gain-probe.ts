export interface GainLevels {
  readonly frames: number;
  /** P90 over all RMS frames; voicedDb is the median above the speech floor. */
  readonly speechDb: number;
  readonly voicedDb: number;
}
export interface ShareGainState {
  readonly sameCapture: boolean;
  readonly sameConstraints: boolean;
  readonly sameSettings: boolean;
  readonly rawGraphs: number;
  readonly outgoingGraphs: number;
  readonly voiceGainGraphs: number;
  readonly inputGain: number;
  readonly gainValues: readonly number[];
  readonly voiceElements: number;
  readonly screenElements: number;
  readonly silentVoiceElements: boolean;
  readonly rawFlags: {
    readonly echoCancellation?: boolean;
    readonly autoGainControl?: boolean;
    readonly noiseSuppression?: boolean;
    readonly sampleRate?: number;
    readonly channelCount?: number;
  };
}
export interface ShareGainSample {
  readonly state: ShareGainState;
  readonly levels: Readonly<Record<'raw' | 'outgoing' | 'decoded' | 'output', GainLevels>>;
  readonly measuredOutputGainDb: number;
  readonly stereo?: {
    readonly frames: number;
    readonly validFrames: number;
    readonly leftMarkerDb: number;
    readonly rightMarkerDb: number;
    readonly leftSeparationDb: number;
    readonly rightSeparationDb: number;
    readonly leftNonMarkerDb: number;
    readonly rightNonMarkerDb: number;
  };
}
export interface VoiceShareGainProbe {
  checkpoint(): ShareGainState;
  state(): ShareGainState;
  screenReady(): boolean;
  sample(duration: number): Promise<ShareGainSample>;
  relay(): Promise<readonly { readonly type: string; readonly protocol: string }[]>;
  close(): void;
}

/** Observe the production capture, processor and SDK graphs without replacing their tracks.
 * Only aggregate levels and equality checks leave the browser; capture IDs stay private.
 */
export function installVoiceShareGainProbe(): void {
  type Stage = 'raw' | 'outgoing' | 'decoded' | 'output' | 'screen';
  type Role = { stage: 'raw' | 'decoded' | 'screen'; track: MediaStreamTrack };
  type Meter = {
    stage: Stage;
    track: MediaStreamTrack;
    node: AudioNode;
    analyser: AnalyserNode;
    data: Float32Array<ArrayBuffer>;
    gain?: GainNode;
    channels?: readonly AnalyserNode[];
  };
  const meters: Meter[] = [];
  const peers: RTCPeerConnection[] = [];
  const roles = new WeakMap<AudioNode, Role>();
  const voiceNodes = new WeakSet<AudioNode>();
  const voiceContexts = new WeakSet<BaseAudioContext>();
  const settings = new WeakMap<BaseAudioContext, { inputGain: number }>();
  let baseline: { track: MediaStreamTrack; id: string; constraints: string; settings: string } | undefined;
  const connect = AudioNode.prototype.connect;
  const createSource = AudioContext.prototype.createMediaStreamSource;
  const Worklet = window.AudioWorkletNode;
  const Peer = window.RTCPeerConnection;
  const canonical = (value: unknown): string => {
    if (value && typeof value === 'object' && !Array.isArray(value))
      return JSON.stringify(
        Object.fromEntries(
          Object.entries(value)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([key, nested]) => [key, JSON.parse(canonical(nested))]),
        ),
      );
    return JSON.stringify(value);
  };
  const audioElements = (kind: 'voice' | 'screen') => [
    ...document.querySelectorAll<HTMLAudioElement>(`audio[data-source="${kind}"]`),
  ];
  const elementTrack = (element: HTMLAudioElement) =>
    (element.srcObject as MediaStream | null)?.getAudioTracks()[0];
  const active = (meter: Meter): boolean => {
    if (meter.track.readyState !== 'live') return false;
    if (meter.stage === 'raw' || meter.stage === 'outgoing')
      return meters.filter((candidate) => candidate.stage === meter.stage).at(-1) === meter;
    const kind = meter.stage === 'screen' ? 'screen' : 'voice';
    return (
      audioElements(kind).some((element) => elementTrack(element)?.id === meter.track.id) &&
      meters
        .filter((candidate) => candidate.stage === meter.stage && candidate.track === meter.track)
        .at(-1) === meter
    );
  };
  const meterFor = (stage: Stage) => meters.filter((meter) => meter.stage === stage && active(meter)).at(-1);
  const addMeter = (node: AudioNode, stage: Stage, track: MediaStreamTrack, gain?: GainNode) => {
    const analyser = node.context.createAnalyser();
    analyser.fftSize = 2048;
    Reflect.apply(connect, node, [analyser]);
    const meter: Meter = {
      stage,
      track,
      node,
      analyser,
      data: new Float32Array(analyser.fftSize),
      gain,
    };
    if (stage === 'screen') {
      const splitter = node.context.createChannelSplitter(2);
      Reflect.apply(connect, node, [splitter]);
      meter.channels = [0, 1].map((channel) => {
        const analysis = node.context.createAnalyser();
        analysis.fftSize = 4096;
        analysis.smoothingTimeConstant = 0;
        Reflect.apply(connect, splitter, [analysis, channel]);
        return analysis;
      });
    }
    meters.push(meter);
  };
  window.AudioWorkletNode = class extends Worklet {
    constructor(context: BaseAudioContext, name: string, options?: AudioWorkletNodeOptions) {
      super(context, name, options);
      if (name !== 'gul-voice') return;
      voiceNodes.add(this);
      voiceContexts.add(context);
      settings.set(context, { inputGain: Number(options?.processorOptions?.inputGain) });
      const postMessage = this.port.postMessage.bind(this.port);
      this.port.postMessage = ((...args: Parameters<MessagePort['postMessage']>) => {
        const message = args[0] as { type?: string; settings?: { inputGain: number } } | undefined;
        if (message?.type === 'settings' && message.settings)
          settings.set(context, { inputGain: message.settings.inputGain });
        Reflect.apply(postMessage, this.port, args);
      }) as MessagePort['postMessage'];
    }
  };
  AudioContext.prototype.createMediaStreamSource = function (stream: MediaStream) {
    const source = createSource.call(this, stream);
    const track = stream.getAudioTracks()[0];
    if (!track) return source;
    const element = [...audioElements('voice'), ...audioElements('screen')].find(
      (candidate) => elementTrack(candidate)?.id === track.id,
    );
    const stage = element
      ? element.dataset.source === 'screen'
        ? 'screen'
        : 'decoded'
      : voiceContexts.has(this) && track.getSettings().deviceId
        ? 'raw'
        : undefined;
    if (stage) {
      roles.set(source, { stage, track });
      addMeter(source, stage, track);
    }
    return source;
  };
  AudioNode.prototype.connect = function (this: AudioNode, ...args: unknown[]) {
    const result = Reflect.apply(connect, this, args);
    const destination = args[0];
    const role = roles.get(this);
    if (destination instanceof AudioNode && role) roles.set(destination, role);
    if (voiceNodes.has(this) && destination instanceof MediaStreamAudioDestinationNode) {
      const track = destination.stream.getAudioTracks()[0];
      if (track) addMeter(this, 'outgoing', track);
    }
    if (this instanceof GainNode && destination instanceof AudioDestinationNode && role?.stage === 'decoded')
      addMeter(this, 'output', role.track, this);
    return result;
  } as AudioNode['connect'];
  window.RTCPeerConnection = class extends Peer {
    constructor(config?: RTCConfiguration) {
      super(config);
      peers.push(this);
    }
  };
  const state = (): ShareGainState => {
    const raw = meterFor('raw');
    const flags = raw?.track.getSettings();
    const voice = audioElements('voice');
    return {
      sameCapture: Boolean(raw && baseline && baseline.track === raw.track && baseline.id === raw.track.id),
      sameConstraints: Boolean(raw && baseline?.constraints === canonical(raw.track.getConstraints())),
      sameSettings: Boolean(raw && baseline?.settings === canonical(raw.track.getSettings())),
      rawGraphs: meters.filter((meter) => meter.stage === 'raw').length,
      outgoingGraphs: meters.filter((meter) => meter.stage === 'outgoing').length,
      voiceGainGraphs: meters.filter((meter) => meter.stage === 'output').length,
      inputGain: raw ? (settings.get(raw.node.context)?.inputGain ?? Number.NaN) : Number.NaN,
      gainValues: meters
        .filter((meter) => meter.stage === 'output' && active(meter))
        .map((meter) => meter.gain!.gain.value),
      voiceElements: voice.length,
      screenElements: audioElements('screen').length,
      silentVoiceElements: voice.every((element) => element.muted && element.volume === 0),
      rawFlags: {
        echoCancellation: flags?.echoCancellation === true,
        autoGainControl: flags?.autoGainControl,
        noiseSuppression: flags?.noiseSuppression,
        sampleRate: flags?.sampleRate,
        channelCount: flags?.channelCount,
      },
    };
  };
  const quantile = (values: readonly number[], fraction: number) => {
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * fraction))] ?? 0;
  };
  const db = (value: number) => 20 * Math.log10(Math.max(1e-8, value));
  const rms = (meter: Meter) => {
    meter.analyser.getFloatTimeDomainData(meter.data);
    return Math.sqrt(meter.data.reduce((energy, sample) => energy + sample * sample, 0) / meter.data.length);
  };
  const levels = (values: readonly number[]): GainLevels => {
    const peak = quantile(values, 0.9);
    const voiced = values.filter((value) => value > Math.max(1e-5, peak * 0.15));
    return { frames: values.length, speechDb: db(peak), voicedDb: db(quantile(voiced, 0.5)) };
  };
  const spectrum = (analyser: AnalyserNode) => {
    const bins = new Float32Array(analyser.frequencyBinCount);
    analyser.getFloatFrequencyData(bins);
    const markerPower = (frequency: number) => {
      const center = Math.round((frequency * analyser.fftSize) / analyser.context.sampleRate);
      return [center - 2, center - 1, center, center + 1, center + 2].reduce(
        (sum, index) => sum + 10 ** (bins[index] / 10),
        0,
      );
    };
    let marked = 0;
    let unmarked = 0;
    bins.forEach((value, index) => {
      const frequency = (index * analyser.context.sampleRate) / analyser.fftSize;
      if (frequency < 100 || frequency > 6000) return;
      const power = 10 ** (value / 10);
      if (Math.abs(frequency - 440) <= 100 || Math.abs(frequency - 880) <= 100) marked += power;
      else unmarked += power;
    });
    return {
      audible: marked + unmarked > 1e-12,
      markerDb: 10 * Math.log10(Math.max(1e-20, marked)),
      separationDb: 10 * Math.log10(Math.max(1e-20, markerPower(440)) / Math.max(1e-20, markerPower(880))),
      nonMarkerDb: 10 * Math.log10(Math.max(1e-20, unmarked) / Math.max(1e-20, marked)),
    };
  };
  const probe: VoiceShareGainProbe = {
    checkpoint() {
      const raw = meterFor('raw');
      if (!raw || !meterFor('outgoing') || !meterFor('decoded') || !meterFor('output'))
        throw new Error('Production audio probe unavailable.');
      baseline = {
        track: raw.track,
        id: raw.track.id,
        constraints: canonical(raw.track.getConstraints()),
        settings: canonical(raw.track.getSettings()),
      };
      return state();
    },
    state,
    screenReady() {
      const channels = meterFor('screen')?.channels;
      return Boolean(channels?.every((channel) => spectrum(channel).markerDb > -60));
    },
    async sample(duration) {
      if (!Number.isFinite(duration) || duration < 1000 || duration > 20_000)
        throw new Error('Invalid gain sample duration.');
      const samples = {
        raw: [] as number[],
        outgoing: [] as number[],
        decoded: [] as number[],
        output: [] as number[],
      };
      const ratios: number[] = [];
      const markers: Omit<NonNullable<ShareGainSample['stereo']>, 'frames' | 'validFrames'>[] = [];
      let screenFrames = 0;
      const deadline = performance.now() + duration;
      while (performance.now() < deadline) {
        const frame: Partial<Record<keyof typeof samples, number>> = {};
        for (const stage of ['raw', 'outgoing', 'decoded', 'output'] as const) {
          const meter = meterFor(stage);
          if (!meter) continue;
          frame[stage] = rms(meter);
          samples[stage].push(frame[stage]);
        }
        if ((frame.decoded ?? 0) > 0.0001 && (frame.output ?? 0) > 0)
          ratios.push(db(frame.output! / frame.decoded!));
        const channels = meterFor('screen')?.channels;
        if (channels) {
          const [left, right] = channels.map(spectrum);
          screenFrames++;
          // Pure silence has no defined energy ratio. Broadband audio without
          // markers remains valid and must fail the leakage and marker checks.
          if (left.audible && right.audible)
            markers.push({
              leftMarkerDb: left.markerDb,
              rightMarkerDb: right.markerDb,
              leftSeparationDb: left.separationDb,
              rightSeparationDb: -right.separationDb,
              leftNonMarkerDb: left.nonMarkerDb,
              rightNonMarkerDb: right.nonMarkerDb,
            });
        }
        await new Promise((resolve) => setTimeout(resolve, 25));
      }
      return {
        state: state(),
        levels: {
          raw: levels(samples.raw),
          outgoing: levels(samples.outgoing),
          decoded: levels(samples.decoded),
          output: levels(samples.output),
        },
        measuredOutputGainDb: quantile(ratios, 0.5),
        ...(screenFrames
          ? {
              stereo: {
                frames: screenFrames,
                validFrames: markers.length,
                leftMarkerDb: quantile(
                  markers.map((marker) => marker.leftMarkerDb),
                  0.1,
                ),
                rightMarkerDb: quantile(
                  markers.map((marker) => marker.rightMarkerDb),
                  0.1,
                ),
                leftSeparationDb: quantile(
                  markers.map((marker) => marker.leftSeparationDb),
                  0.5,
                ),
                rightSeparationDb: quantile(
                  markers.map((marker) => marker.rightSeparationDb),
                  0.5,
                ),
                leftNonMarkerDb: quantile(
                  markers.map((marker) => marker.leftNonMarkerDb),
                  0.9,
                ),
                rightNonMarkerDb: quantile(
                  markers.map((marker) => marker.rightNonMarkerDb),
                  0.9,
                ),
              },
            }
          : {}),
      };
    },
    async relay() {
      const selected: { type: string; protocol: string }[] = [];
      for (const peer of peers) {
        if (peer.connectionState !== 'connected') continue;
        const report = await peer.getStats();
        report.forEach((entry) => {
          if (entry.type !== 'transport' || !entry.selectedCandidatePairId) return;
          const pair = report.get(entry.selectedCandidatePairId);
          const local = pair && report.get(pair.localCandidateId);
          if (local) selected.push({ type: local.candidateType, protocol: local.relayProtocol });
        });
      }
      return selected;
    },
    close() {
      AudioNode.prototype.connect = connect;
      AudioContext.prototype.createMediaStreamSource = createSource;
      window.AudioWorkletNode = Worklet;
      window.RTCPeerConnection = Peer;
      meters.forEach((meter) => meter.analyser.disconnect());
    },
  };
  Object.defineProperty(window, '__gulVoiceShareGainProbe', { value: probe });
}

/** Synthetic screen content leaves microphone capture and Chromium speech processing intact.
 * Linux still obtains production source consent and native process audio exclusion.
 */
export function installShareGainScreenFixture(linux: boolean): void {
  const nativeDisplay = navigator.mediaDevices.getDisplayMedia.bind(navigator.mediaDevices);
  navigator.mediaDevices.getDisplayMedia = async (options) => {
    const native = linux ? await nativeDisplay(options) : undefined;
    const canvas = document.createElement('canvas');
    canvas.width = 640;
    canvas.height = 360;
    const painter = canvas.getContext('2d')!;
    let frame = 0;
    const timer = setInterval(() => {
      painter.fillStyle = `hsl(${(frame++ * 13) % 360} 60% 40%)`;
      painter.fillRect(0, 0, 640, 360);
      painter.fillStyle = '#ffffff';
      painter.fillRect((frame * 11) % 600, 150, 40, 40);
    }, 33);
    const stream = canvas.captureStream(30);
    let audio: AudioContext | undefined;
    if (!linux) {
      audio = new AudioContext({ sampleRate: 48000 });
      const output = audio.createMediaStreamDestination();
      const stereo = audio.createChannelMerger(2);
      [440, 880].forEach((frequency, channel) => {
        const oscillator = audio!.createOscillator();
        const gain = audio!.createGain();
        oscillator.frequency.value = frequency;
        gain.gain.value = 0.08;
        oscillator.connect(gain).connect(stereo, 0, channel);
        oscillator.start();
      });
      stereo.connect(output);
      void audio.resume();
      stream.addTrack(output.stream.getAudioTracks()[0]);
    }
    const video = stream.getVideoTracks()[0];
    const stop = video.stop.bind(video);
    video.stop = () => {
      clearInterval(timer);
      stop();
      native?.getTracks().forEach((track) => track.stop());
      void audio?.close();
    };
    return stream;
  };
}
