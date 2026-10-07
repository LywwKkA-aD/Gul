/** Serialized into the test renderer; production getUserMedia guards remain locked.
 * Only known test device labels receive a tone. A deviceId alone also matches WebAudio outputs.
 */
export function installMicrophoneCalibration(linux: boolean): void {
  const sourceFor = AudioContext.prototype.createMediaStreamSource;
  AudioContext.prototype.createMediaStreamSource = function (stream: MediaStream) {
    const input = stream.getAudioTracks()[0];
    if (
      !input ||
      !input.getSettings().deviceId ||
      input.label.startsWith('Gul-Screen-Audio-') ||
      (linux ? !/^Gul-Test-Microphone-[01]$/u.test(input.label) : !input.label.includes('Fake'))
    )
      return Reflect.apply(sourceFor, this, [stream]) as MediaStreamAudioSourceNode;
    const destination = this.createMediaStreamDestination();
    const oscillator = this.createOscillator();
    const gain = this.createGain();
    oscillator.frequency.value = 330;
    gain.gain.value = 0.1;
    oscillator.connect(gain).connect(destination);
    oscillator.start();
    const source = Reflect.apply(sourceFor, this, [destination.stream]) as MediaStreamAudioSourceNode;
    const disconnect = source.disconnect.bind(source);
    let stopped = false;
    source.disconnect = () => {
      disconnect();
      if (stopped) return;
      stopped = true;
      oscillator.stop();
      oscillator.disconnect();
      gain.disconnect();
      destination.stream.getTracks().forEach((track) => track.stop());
    };
    return source;
  };
}
