import { copyFile, lstat, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const versionPattern = /^\d+\.\d+\.\d+(?:-[a-zA-Z0-9]+(?:\.[a-zA-Z0-9]+)*)?$/u;
export function validateReleaseTag(tag, version) {
  if (!versionPattern.test(version) || tag !== `v${version}`)
    throw new Error('Release tag differs from client version.');
}
export async function collectArtifacts(directory, version, platform, arch) {
  validateReleaseTag(`v${version}`, version);
  const os = { win32: 'win', linux: 'linux', darwin: 'mac' }[platform];
  if (!os || !['x64', 'arm64'].includes(arch)) throw new Error('Unsupported release platform.');
  const extensions = platform === 'win32' ? ['exe', 'zip'] : platform === 'linux' ? ['deb'] : ['dmg'];
  const names = extensions.map((ext) => `Gul-${version}-${os}-${arch}.${ext}`);
  for (const name of names) {
    const info = await lstat(join(directory, name));
    if (!info.isFile() || info.size === 0) throw new Error('Installer is missing or invalid.');
  }
  const output = join(directory, 'release');
  await rm(output, { recursive: true, force: true });
  await mkdir(output, { recursive: true });
  for (const name of names) {
    const digest = createHash('sha256')
      .update(await readFile(join(directory, name)))
      .digest('hex');
    await copyFile(join(directory, name), join(output, name));
    await writeFile(join(output, `${name}.sha256`), `${digest}  ${name}\n`);
  }
  return names;
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const { version } = JSON.parse(await readFile('package.json', 'utf8'));
  if (process.argv[2] === '--check-tag') validateReleaseTag(process.argv[3], version);
  else if (process.argv[2] === '--collect') {
    const names = await collectArtifacts('artifacts', version, process.platform, process.arch);
    process.stdout.write(`Prepared ${names.length} checked release artifacts.\n`);
  } else throw new Error('Use --check-tag TAG or --collect.');
}
