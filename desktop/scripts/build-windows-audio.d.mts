export function windowsAudioEnvironmentCommand(path: string): string;
export function windowsAudioCompileArguments(source: string, output: string, object: string): string[];
export function buildWindowsAudio(): Promise<string | undefined>;
