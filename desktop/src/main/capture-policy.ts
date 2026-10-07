export interface CaptureCapabilities {
  readonly platform: string;
  readonly backend: 'wasapi' | 'pipewire-pulse' | 'coreaudio' | 'none';
  readonly systemAudio: boolean;
  readonly ownAudioExcluded: boolean;
  readonly audioServer: 'detected' | 'not-detected' | 'not-required';
  readonly details: string;
}

/** Capabilities of the pinned Electron44/Chromium152 implementation, not a promise of audible samples. */
export function captureCapabilities(
  platform: string,
  release: string,
  pulseDetected: boolean,
): CaptureCapabilities {
  const components = release.split('.').map(Number);
  const windows11 = platform === 'win32' && components[0] === 10 && components[2] >= 22000;
  const coreaudio =
    platform === 'darwin' && (components[0] > 23 || (components[0] === 23 && components[1] >= 2));
  const systemAudio = platform === 'win32' || platform === 'linux' || coreaudio;
  const ownAudioExcluded = windows11 || coreaudio;
  return Object.freeze({
    platform,
    backend:
      platform === 'win32'
        ? 'wasapi'
        : platform === 'linux'
          ? 'pipewire-pulse'
          : coreaudio
            ? 'coreaudio'
            : 'none',
    systemAudio,
    ownAudioExcluded,
    audioServer: platform === 'linux' ? (pulseDetected ? 'detected' : 'not-detected') : 'not-required',
    details: !systemAudio
      ? 'Этот режим передаёт изображение. Системный звук не поддерживается этой версией ОС.'
      : ownAudioExcluded
        ? 'Передаётся общий системный звук. Gul запрашивает исключение собственного воспроизведения; это зависит от возможностей ОС и источника.'
        : 'Передаётся весь системный звук, включая голоса из Gul. Чтобы не возвращать голоса собеседникам, выключите входящий звук Gul на время демонстрации или выберите для Gul другое устройство вывода.' +
          (platform === 'linux' && !pulseDetected
            ? ' Аудиосервер не найден: проверьте работу PipeWire/PulseAudio.'
            : ''),
  });
}

interface Source {
  readonly id: string;
  readonly name: string;
}
export interface CaptureChoice {
  readonly response: number;
  readonly checkboxChecked: boolean;
}
export interface CaptureSelection<S extends Source> {
  readonly video: S;
  readonly audio?: 'loopback';
}
export interface CaptureOptions<S extends Source> {
  readonly valid: () => boolean;
  readonly getSources: () => Promise<readonly S[]>;
  readonly pick: (sources: readonly S[], audio: boolean, details: string) => Promise<CaptureChoice>;
  readonly capabilities: CaptureCapabilities;
  readonly audioRequested: boolean;
}

/** The broker epoch is checked around both asynchronous user-consent boundaries. */
export class CaptureChooser {
  private choosing = false;
  async choose<S extends Source>(options: CaptureOptions<S>): Promise<CaptureSelection<S> | null> {
    if (this.choosing || !options.valid()) return null;
    this.choosing = true;
    try {
      const sources = await options.getSources();
      if (!options.valid() || !sources.length) return null;
      const audio = options.audioRequested && options.capabilities.systemAudio;
      const choice = await options.pick(sources, audio, options.capabilities.details);
      if (
        !options.valid() ||
        !Number.isInteger(choice.response) ||
        choice.response < 1 ||
        choice.response > sources.length
      )
        return null;
      return Object.freeze({
        video: sources[choice.response - 1],
        ...(audio && choice.checkboxChecked ? { audio: 'loopback' as const } : {}),
      });
    } finally {
      this.choosing = false;
    }
  }
}
