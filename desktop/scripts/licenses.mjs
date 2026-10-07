import { copyFile, mkdir, readFile, readdir, writeFile, rm } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';

async function packageFor(input) {
  let directory = dirname(resolve(input));
  while (directory.includes('node_modules')) {
    try {
      const metadata = JSON.parse(await readFile(join(directory, 'package.json'), 'utf8'));
      if (metadata.name && metadata.version) return { directory, metadata };
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    directory = dirname(directory);
  }
}

export async function collectLicenses(bundles) {
  await rm('dist/licenses', { recursive: true, force: true });
  await mkdir('dist/licenses', { recursive: true });
  const packages = new Map();
  for (const bundle of bundles) {
    for (const input of Object.keys(bundle.metafile.inputs)) {
      if (!input.includes('node_modules/')) continue;
      const pkg = await packageFor(input);
      if (pkg) packages.set(`${pkg.metadata.name}@${pkg.metadata.version}`, pkg);
    }
  }
  const manifest = [];
  for (const [identity, { directory, metadata }] of [...packages.entries()].sort()) {
    const output = join('dist/licenses/npm', metadata.name, metadata.version);
    await mkdir(output, { recursive: true });
    const files = (await readdir(directory)).filter((name) =>
      /^(license|licence|copying|notice)(\.|$)/i.test(name),
    );
    if (metadata.name === '@bufbuild/protobuf') {
      if (metadata.version !== '1.10.1') throw new Error('Review protobuf attribution before upgrading.');
      await copyFile(
        '../third_party/npm-attributions/bufbuild-protobuf-1.10.1/LICENSE-APACHE-2.0',
        join(output, 'LICENSE-APACHE-2.0'),
      );
      await copyFile(
        join(directory, 'dist/esm/google/varint.js'),
        join(output, 'Google-BSD-3-Clause-LICENSE-and-source.js'),
      );
      await copyFile(join(directory, 'dist/esm/index.js'), join(output, 'Buf-NOTICE-and-source.js'));
    } else {
      if (!files.length) throw new Error(`Missing bundled dependency license: ${identity}`);
      for (const file of files) await copyFile(join(directory, file), join(output, file));
    }
    if (metadata.name === '@jitsi/rnnoise-wasm') {
      if (metadata.version !== '0.2.1') throw new Error('Review RNNoise attribution before upgrading.');
      await copyFile(
        '../third_party/npm-attributions/jitsi-rnnoise-wasm-0.2.1/LICENSE-RNNOISE-BSD-3-Clause',
        join(output, 'LICENSE-RNNOISE-BSD-3-Clause'),
      );
    }
    manifest.push({
      name: metadata.name,
      version: metadata.version,
      license: metadata.name === '@jitsi/rnnoise-wasm' ? 'Apache-2.0 AND BSD-3-Clause' : metadata.license,
    });
  }
  await writeFile('dist/licenses/manifest.json', JSON.stringify(manifest, null, 2) + '\n');
}
