/** Isolated device-audio A/B. Production capture guards and media settings stay unchanged. */
export interface VoicePipeline {
  readonly browserNoise: boolean;
  readonly neuralNoise: boolean;
  readonly automaticGain: boolean;
}

export function installVoicePipelineProbe() {
  interface Pipeline {
    browserNoise: boolean;
    neuralNoise: boolean;
    automaticGain: boolean;
  }
  interface Owned {
    sender: RTCRtpSender;
    original: MediaStreamTrack;
    raw: MediaStreamTrack;
    output: MediaStreamTrack;
    context: AudioContext;
    source: MediaStreamAudioSourceNode;
    node: AudioWorkletNode;
    neuralNoise: boolean;
  }
  const connections = new Set<RTCPeerConnection>();
  const captured = new Set<MediaStreamTrack>();
  const createSource = AudioContext.prototype.createMediaStreamSource;
  AudioContext.prototype.createMediaStreamSource = function (stream: MediaStream) {
    const track = stream.getAudioTracks()[0];
    if (track?.label.includes('Fake') && track.getSettings().deviceId) captured.add(track);
    return createSource.call(this, stream);
  };
  const NativePeer = window.RTCPeerConnection;
  window.RTCPeerConnection = class extends NativePeer {
    constructor(configuration?: RTCConfiguration) {
      super(configuration);
      connections.add(this);
    }
  };
  let current: Owned | undefined;
  const pcm: { time: number; samples: Float32Array }[] = [];
  let receiver:
    { context: AudioContext; source: MediaStreamAudioSourceNode; tap: ScriptProcessorNode } | undefined;
  function flags() {
    if (!current) throw new Error('Missing isolated voice pipeline.');
    const settings = current.raw.getSettings();
    return {
      neuralNoise: current.neuralNoise,
      sampleRate: current.context.sampleRate,
      browserNoise: settings.noiseSuppression,
      automaticGain: settings.autoGainControl,
      echoCancellation: settings.echoCancellation,
      channelCount: settings.channelCount,
      processedChannels: current.output.getSettings().channelCount,
    };
  }
  async function release(owned: Owned, restore: boolean) {
    if (restore && owned.sender.track === owned.output && owned.original.readyState === 'live')
      await owned.sender.replaceTrack(owned.original);
    owned.raw.stop();
    owned.output.stop();
    owned.node.port.postMessage({ type: 'destroy' });
    owned.node.port.close();
    owned.node.disconnect();
    owned.source.disconnect();
    await owned.context.close();
  }
  Object.defineProperty(window, '__gulVoicePipelineProbe', {
    value: {
      flags,
      async beginPCM() {
        if (!receiver) {
          const element = document.querySelector<HTMLAudioElement>('audio[data-source="voice"]');
          const stream = element?.srcObject as MediaStream | undefined;
          if (!stream?.getAudioTracks().length) throw new Error('Missing isolated decoded voice.');
          const context = new AudioContext({ sampleRate: 48000 });
          const source = createSource.call(context, stream);
          const tap = context.createScriptProcessor(2048, 1, 1);
          const silent = context.createGain();
          silent.gain.value = 0;
          tap.onaudioprocess = ({ inputBuffer }) => {
            pcm.push({ time: performance.now(), samples: inputBuffer.getChannelData(0).slice() });
            if (pcm.length > 480) pcm.shift();
          };
          source.connect(tap).connect(silent).connect(context.destination);
          receiver = { context, source, tap };
          await context.resume();
        }
        pcm.length = 0;
        return performance.now();
      },
      readPCM(since: number) {
        return pcm.filter((frame) => frame.time >= since).flatMap((frame) => [...frame.samples]);
      },
      async replace(policy: Pipeline) {
        const previous = current;
        const sender =
          previous?.sender ??
          [...connections]
            .filter((connection) => connection.connectionState === 'connected')
            .flatMap((connection) => connection.getSenders())
            .find((candidate) => candidate.track?.kind === 'audio');
        if (!sender?.track) throw new Error('Missing isolated voice sender.');
        const original = previous?.original ?? sender.track;
        // Chromium reuses an open device's processing configuration. Close every
        // owned fake input before recapture so getSettings proves this policy.
        captured.forEach((track) => track.stop());
        captured.clear();
        const stream = await navigator.mediaDevices.getUserMedia({
          audio: {
            channelCount: 1,
            sampleRate: 48000,
            echoCancellation: true,
            noiseSuppression: policy.browserNoise,
            autoGainControl: policy.automaticGain,
          },
          video: false,
        });
        const raw = stream.getAudioTracks()[0];
        // Never run this benchmark against a host microphone.
        if (!raw?.label.includes('Fake')) {
          stream.getTracks().forEach((track) => track.stop());
          throw new Error('Isolated fake audio device required.');
        }
        raw.enabled = false;
        const context = new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' });
        let owned: Owned | undefined;
        try {
          if (context.sampleRate !== 48000) throw new Error('Expected 48k audio context.');
          await context.audioWorklet.addModule(new URL('voice-worklet.js', location.href).href);
          const node = new AudioWorkletNode(context, 'gul-voice', {
            numberOfInputs: 1,
            numberOfOutputs: 1,
            outputChannelCount: [1],
            processorOptions: {
              mode: 'continuous',
              thresholdDb: -45,
              holdMs: 250,
              inputGain: 1,
              echoCancellation: true,
              noiseSuppression: policy.neuralNoise,
              autoGainControl: policy.automaticGain,
            },
          });
          const ready = await new Promise<{ neuralNoise: boolean; sampleRate: number }>((resolve, reject) => {
            const timeout = setTimeout(() => reject(new Error('Worklet did not become ready.')), 2000);
            node.port.addEventListener('message', ({ data }) => {
              if (data?.type !== 'ready') return;
              clearTimeout(timeout);
              resolve({ neuralNoise: data.neuralNoise, sampleRate: data.sampleRate });
            });
            node.port.start();
          });
          if (ready.neuralNoise !== policy.neuralNoise || ready.sampleRate !== 48000)
            throw new Error('Pipeline policy did not apply.');
          const source = context.createMediaStreamSource(stream);
          const destination = context.createMediaStreamDestination();
          destination.channelCount = 1;
          const output = destination.stream.getAudioTracks()[0];
          owned = { sender, original, raw, output, context, source, node, neuralNoise: ready.neuralNoise };
          source.connect(node).connect(destination);
          await context.resume();
          raw.enabled = true;
          await sender.replaceTrack(output);
          current = owned;
          if (previous) await release(previous, false);
          return flags();
        } catch {
          if (owned) await release(owned, true);
          else {
            raw.stop();
            await context.close();
          }
          throw new Error('Isolated pipeline preparation failed.');
        }
      },
      async close() {
        const owned = current;
        current = undefined;
        if (owned) await release(owned, true);
        if (receiver) {
          receiver.tap.onaudioprocess = null;
          receiver.tap.disconnect();
          receiver.source.disconnect();
          await receiver.context.close();
          receiver = undefined;
        }
        pcm.length = 0;
      },
    },
  });
}
