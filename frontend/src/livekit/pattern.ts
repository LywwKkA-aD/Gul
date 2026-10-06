import { LocalAudioTrack, LocalVideoTrack } from 'livekit-client';
import type { ScreenCapture } from './controller';

// A deterministic source tests transport/decoding separately from OS capture
// permissions. Nothing is connected to the sender's speakers or microphone.
export async function createTestPattern(): Promise<ScreenCapture> {
  const canvas = document.createElement('canvas');
  canvas.width = 1280;
  canvas.height = 720;
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Canvas unavailable');
  let frame = 0;
  const draw = () => {
    context.fillStyle = '#1B2440';
    context.fillRect(0, 0, 1280, 720);
    context.fillStyle = '#F3F5F9';
    context.font = '40px monospace';
    context.fillText('GUL / LOCAL MEDIA TEST', 64, 90);
    context.font = '28px monospace';
    context.fillText(`Frame ${frame++} / 720p 30 / audio 440 Hz`, 64, 148);
    context.fillStyle = '#2F52DE';
    context.fillRect((frame * 8) % 1160, 290, 120, 120);
    context.fillStyle = '#B9C1D4';
    context.fillText(new Date().toISOString(), 64, 650);
  };
  draw();
  const stream = canvas.captureStream(30);
  let audio: AudioContext | undefined;
  let destination: MediaStreamAudioDestinationNode | undefined;
  let oscillator: OscillatorNode | undefined;
  let gain: GainNode | undefined;
  let timer: number | undefined;
  let oscillatorStarted = false;
  let closed = false;
  const cleanup = () => {
    if (closed) return;
    closed = true;
    if (timer !== undefined) window.clearInterval(timer);
    stream.getTracks().forEach((track) => track.stop());
    destination?.stream.getTracks().forEach((track) => track.stop());
    if (oscillatorStarted) oscillator?.stop();
    oscillator?.disconnect();
    gain?.disconnect();
    void audio?.close().catch(() => {});
  };
  try {
    audio = new AudioContext({ sampleRate: 48000 });
    destination = audio.createMediaStreamDestination();
    oscillator = audio.createOscillator();
    gain = audio.createGain();
    gain.gain.value = 0.06;
    oscillator.frequency.value = 440;
    oscillator.connect(gain).connect(destination);
    oscillator.start();
    oscillatorStarted = true;
    timer = window.setInterval(draw, 1000 / 30);
    await audio.resume();
    return {
      tracks: [new LocalVideoTrack(stream.getVideoTracks()[0]), new LocalAudioTrack(destination.stream.getAudioTracks()[0])],
      cleanup,
    };
  } catch (error) {
    cleanup();
    throw error;
  }
}
