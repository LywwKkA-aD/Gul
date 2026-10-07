export function vcvarsCommand(path: string): string;
export function compileArguments(source: string, output: string, object: string): string[];
export function buildWindowsPTT(): Promise<string | undefined>;
export function linuxCompileArguments(source: string, output: string, gioFlags: readonly string[]): string[];
export function buildLinuxPTT(): Promise<string | undefined>;
