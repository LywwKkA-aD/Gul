export function windowsAudioEnvironmentCommand(path: string): string;
export function windowsAudioCompileArguments(source: string, output: string, object: string): string[];
export function buildWindowsAudio(): Promise<string | undefined>;
export function probeWindowsAudio(
  executable: string,
  execute?: (
    file: string,
    args: readonly string[],
    options: {
      stdio: readonly string[];
      timeout: number;
      maxBuffer: number;
      encoding: string;
      windowsHide: boolean;
    },
  ) => string,
): boolean;
