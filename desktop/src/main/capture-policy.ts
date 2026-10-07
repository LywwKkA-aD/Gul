export interface CaptureCapabilities {
  readonly platform: string;
  readonly backend: 'wasapi' | 'pipewire-pulse' | 'coreaudio' | 'none';
  readonly systemAudio: boolean;
  readonly ownAudioExcluded: boolean;
  readonly audioServer: 'detected' | 'not-detected' | 'not-required';
  readonly details: string;
  readonly picker: 'portal' | 'application';
}

/** Audio support follows native activation; an OS version alone cannot guarantee exclusion. */
export function captureCapabilities(
  platform: string,
  release: string,
  pulseDetected: boolean,
  linuxExcludedAudio = false,
  picker: 'portal' | 'application' = 'application',
  windowsExcludedAudio = false,
): CaptureCapabilities {
  const components = release.split('.').map(Number);
  const coreaudio =
    platform === 'darwin' && (components[0] > 23 || (components[0] === 23 && components[1] >= 2));
  const excludedLinux = platform === 'linux' && pulseDetected && linuxExcludedAudio;
  const excludedWindows = platform === 'win32' && windowsExcludedAudio;
  const systemAudio = excludedWindows || excludedLinux || coreaudio;
  const ownAudioExcluded = excludedWindows || coreaudio || excludedLinux;
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
    picker,
    audioServer: platform === 'linux' ? (pulseDetected ? 'detected' : 'not-detected') : 'not-required',
    details:
      excludedLinux || excludedWindows
        ? 'Передаются звуки приложений и игр. Голоса, демонстрации и сигналы Gul исключены; собеседников по-прежнему слышно.'
        : platform === 'linux'
          ? 'Системный звук недоступен: встроенный помощник или локальный PipeWire/PulseAudio не готов. Общий микс с голосами Gul не записывается.'
          : platform === 'win32'
            ? 'Передаётся изображение. Windows не разрешила захват звука с исключением Gul; общий микс с голосами собеседников не записывается.'
            : !systemAudio
              ? 'Этот режим передаёт изображение. Системный звук не поддерживается этой версией ОС.'
              : 'Передаётся общий системный звук. Gul запрашивает исключение собственного воспроизведения; это зависит от возможностей ОС и источника.',
  });
}

interface Source {
  readonly id: string;
  readonly name: string;
}
/** Match WebRTC's native backend choice; a portal result already carries OS source consent. */
export function capturePickerMode(
  platform: string,
  environment: Readonly<Record<string, string | undefined>>,
): 'portal' | 'application' {
  return platform === 'linux' &&
    environment.XDG_SESSION_TYPE?.startsWith('wayland') &&
    environment.WAYLAND_DISPLAY !== undefined
    ? 'portal'
    : 'application';
}
export function captureSourceLabel(source: Source, index: number): string {
  return (
    source.name.trim() ||
    `${source.id.startsWith('screen:') ? 'Экран' : source.id.startsWith('window:') ? 'Окно' : 'Источник'} ${index + 1}`
  );
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
  readonly portalSelection?: boolean;
  readonly loopbackAudio?: boolean;
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
      // Electron resolves a delegated PipeWire list only after OnSelection. A rejected
      // portal request is never retried or converted into application consent.
      if (options.portalSelection && sources.length !== 1) return null;
      const choice = options.portalSelection
        ? { response: 1, checkboxChecked: audio }
        : await options.pick(sources, audio, options.capabilities.details);
      if (
        !options.valid() ||
        !Number.isInteger(choice.response) ||
        choice.response < 1 ||
        choice.response > sources.length
      )
        return null;
      return Object.freeze({
        video: sources[choice.response - 1],
        ...(audio && choice.checkboxChecked && options.loopbackAudio !== false
          ? { audio: 'loopback' as const }
          : {}),
      });
    } finally {
      this.choosing = false;
    }
  }
}
