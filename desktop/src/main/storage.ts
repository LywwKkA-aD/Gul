import { constants } from 'node:fs';
import { lstat, mkdir, open, rename, rm } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';

export interface SafeStorageAdapter {
  isEncryptionAvailable(): boolean;
  getSelectedStorageBackend(): string;
  encryptString(plainText: string): Buffer;
  decryptString(encrypted: Buffer): string;
}
export function protectedStorage(storage: SafeStorageAdapter, platform: string): boolean {
  try {
    if (!storage.isEncryptionAvailable()) return false;
    return (
      platform !== 'linux' ||
      ['gnome_libsecret', 'kwallet', 'kwallet5', 'kwallet6'].includes(storage.getSelectedStorageBackend())
    );
  } catch {
    return false;
  }
}

/** A settings file is never followed through a symlink or read without a size bound. */
export async function readBoundedJSON(file: string, maximum = 128 * 1024): Promise<unknown | undefined> {
  try {
    const before = await lstat(file);
    if (!before.isFile() || before.isSymbolicLink() || before.size > maximum) return;
    const handle = await open(file, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    try {
      const current = await handle.stat();
      if (!current.isFile() || current.size > maximum || current.ino !== before.ino) return;
      const bytes = Buffer.alloc(maximum + 1);
      const { bytesRead } = await handle.read(bytes, 0, bytes.length, 0);
      if (bytesRead > maximum) return;
      return JSON.parse(bytes.subarray(0, bytesRead).toString('utf8')) as unknown;
    } finally {
      await handle.close();
    }
  } catch {
    return;
  }
}

/** Atomic replacement prevents concurrent writes from exposing a partial secret document. */
export async function writePrivateJSON(file: string, value: unknown, maximum = 128 * 1024): Promise<boolean> {
  return writePrivateBytes(file, Buffer.from(JSON.stringify(value)), maximum);
}

export async function writePrivateBytes(
  file: string,
  content: Uint8Array,
  maximum = 128 * 1024,
): Promise<boolean> {
  if (content.byteLength > maximum) return false;
  const temporary = join(dirname(file), `.gul-${randomBytes(12).toString('hex')}.tmp`);
  try {
    await mkdir(dirname(file), { recursive: true, mode: 0o700 });
    const handle = await open(temporary, 'wx', 0o600);
    try {
      await handle.writeFile(content);
      await handle.sync();
    } finally {
      await handle.close();
    }
    await rename(temporary, file);
    return true;
  } catch {
    return false;
  } finally {
    await rm(temporary, { force: true }).catch(() => {});
  }
}
