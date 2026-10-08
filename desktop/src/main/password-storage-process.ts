import { execFile, type ChildProcess } from 'node:child_process';
import { constants } from 'node:fs';
import { access, lstat } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { promisify } from 'node:util';

export type NativePasswordStorageState = 'ready' | 'locked' | 'missing' | 'cancelled' | 'unavailable';
export function parsePasswordStorageState(output: string): NativePasswordStorageState {
  for (const state of ['ready', 'locked', 'missing', 'cancelled', 'unavailable'] as const)
    if (output === `GUL_PASSWORD_STORE_${state.toUpperCase()}\n`) return state;
  return 'unavailable';
}
type Execution = Promise<{ stdout: string }> & { readonly child: ChildProcess };
interface Dependencies {
  readonly prepare: (executable: string) => Promise<boolean>;
  readonly launch: (executable: string, args: string[], signal: AbortSignal, timeout: number) => Execution;
}
const dependencies: Dependencies = {
  prepare: async (executable) => {
    const info = await lstat(executable);
    if (!info.isFile() || info.isSymbolicLink() || info.size > 1024 * 1024 || (info.mode & 0o022) !== 0)
      return false;
    await access(executable, constants.X_OK);
    return true;
  },
  launch: (executable, args, signal, timeout) =>
    promisify(execFile)(executable, args, {
      shell: false,
      maxBuffer: 128,
      timeout,
      signal,
      encoding: 'utf8',
      windowsHide: true,
    }),
};

/** No helper may outlive shutdown or start after asynchronous executable validation. */
export async function runPasswordStorageProcess(
  executable: string,
  applicationName: string,
  mode: 'status' | 'unlock',
  signal: AbortSignal,
  children: Set<ChildProcess>,
  implementation: Dependencies = dependencies,
): Promise<NativePasswordStorageState> {
  if (
    signal.aborted ||
    !isAbsolute(executable) ||
    !applicationName ||
    applicationName.length > 128 ||
    /[\u0000-\u001f\u007f]/u.test(applicationName)
  )
    return 'unavailable';
  if (!(await implementation.prepare(executable)) || signal.aborted) return 'unavailable';
  const execution = implementation.launch(
    executable,
    [`--${mode}`, applicationName],
    signal,
    mode === 'status' ? 7000 : 95_000,
  );
  const child = execution.child;
  const release = () => {
    children.delete(child);
    child.removeListener('exit', release);
    child.removeListener('close', release);
  };
  children.add(child);
  child.once('exit', release);
  child.once('close', release);
  if (child.exitCode !== null || child.signalCode !== null) release();
  // AbortError can precede the actual child exit: its lifetime remains tracked by exit/close.
  const { stdout } = await execution;
  return parsePasswordStorageState(stdout);
}

/** Abort sends SIGTERM first so the helper can dismiss its own system prompt. */
export function waitForPasswordStorageExit(
  child: ChildProcess,
  graceMs = 1000,
  killGraceMs = 1000,
): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
  return new Promise((resolve) => {
    let final: ReturnType<typeof setTimeout> | undefined;
    const finish = () => {
      clearTimeout(grace);
      if (final) clearTimeout(final);
      child.removeListener('exit', finish);
      resolve();
    };
    child.once('exit', finish);
    const grace = setTimeout(() => {
      try {
        child.kill('SIGKILL');
      } catch {
        /* The child may have exited concurrently. */
      }
      final = setTimeout(finish, killGraceMs);
    }, graceMs);
  });
}
