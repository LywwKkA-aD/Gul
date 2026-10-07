import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function windowsAudioEnvironmentCommand(path) {
  if (
    typeof path !== 'string' ||
    !/^[A-Za-z]:\\/u.test(path) ||
    !/\.bat$/iu.test(path) ||
    /["%!&|<>^\r\n]/u.test(path)
  )
    throw new Error('Invalid MSVC environment script');
  return `"call "${path}" >nul && set"`;
}
export function windowsAudioCompileArguments(source, output, object) {
  return [
    '/nologo',
    '/std:c++17',
    '/utf-8',
    '/MT',
    '/O2',
    '/W4',
    '/WX',
    '/EHsc',
    '/guard:cf',
    '/GS',
    '/DUNICODE',
    '/D_UNICODE',
    '/D_WIN32_WINNT=0x0A00',
    '/DNOMINMAX',
    `/Fo${object}`,
    `/Fe${output}`,
    source,
    '/link',
    '/SUBSYSTEM:CONSOLE',
    '/DYNAMICBASE',
    '/NXCOMPAT',
    '/HIGHENTROPYVA',
    'ole32.lib',
    'advapi32.lib',
    'mmdevapi.lib',
  ];
}
export function probeWindowsAudio(executable, execute = execFileSync) {
  try {
    return (
      execute(executable, ['--probe'], {
        stdio: ['ignore', 'pipe', 'pipe'],
        timeout: 10000,
        maxBuffer: 64,
        encoding: 'utf8',
        windowsHide: true,
      }) === 'SUPPORTED\n'
    );
  } catch {
    return false;
  }
}
async function compilerEnvironment() {
  const programFiles = process.env['ProgramFiles(x86)'] ?? 'C:\\Program Files (x86)';
  const vswhere = join(programFiles, 'Microsoft Visual Studio', 'Installer', 'vswhere.exe');
  const installation = execFileSync(
    vswhere,
    [
      '-latest',
      '-products',
      '*',
      '-requires',
      'Microsoft.VisualStudio.Component.VC.Tools.x86.x64',
      '-property',
      'installationPath',
    ],
    { encoding: 'utf8', windowsHide: true, maxBuffer: 64 * 1024 },
  ).trim();
  const vcvars = join(installation, 'VC', 'Auxiliary', 'Build', 'vcvars64.bat');
  const content = execFileSync(
    join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'cmd.exe'),
    ['/d', '/s', '/c', windowsAudioEnvironmentCommand(vcvars)],
    {
      encoding: 'utf8',
      windowsHide: true,
      windowsVerbatimArguments: true,
      maxBuffer: 1024 * 1024,
    },
  );
  const environment = { ...process.env };
  for (const line of content.split(/\r?\n/u)) {
    const separator = line.indexOf('=');
    if (separator > 0) environment[line.slice(0, separator)] = line.slice(separator + 1);
  }
  const version = (
    await readFile(
      join(installation, 'VC', 'Auxiliary', 'Build', 'Microsoft.VCToolsVersion.default.txt'),
      'utf8',
    )
  ).trim();
  if (!/^\d+(?:\.\d+){1,3}$/u.test(version)) throw new Error('Invalid MSVC toolchain version');
  return {
    compiler: join(installation, 'VC', 'Tools', 'MSVC', version, 'bin', 'Hostx64', 'x64', 'cl.exe'),
    environment,
  };
}
export async function buildWindowsAudio() {
  if (process.platform !== 'win32') return undefined;
  if (process.arch !== 'x64') throw new Error('Windows audio requires an x64 build host');
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const source = join(root, 'native', 'audio-capture', 'windows');
  const output = join(root, 'resources', 'audio-capture', 'win32-x64', 'gul-audio.exe');
  const temporary = await mkdtemp(join(tmpdir(), 'gul-windows-audio-'));
  try {
    const { compiler, environment } = await compilerEnvironment();
    const options = { cwd: temporary, env: environment, stdio: 'inherit', windowsHide: true };
    const policy = join(temporary, 'protocol-test.exe');
    execFileSync(
      compiler,
      windowsAudioCompileArguments(
        join(source, 'protocol.test.cpp'),
        policy,
        join(temporary, 'protocol.obj'),
      ),
      options,
    );
    execFileSync(policy, [], options);
    const quality = join(temporary, 'quality-test.exe');
    execFileSync(
      compiler,
      windowsAudioCompileArguments(join(source, 'quality.test.cpp'), quality, join(temporary, 'quality.obj')),
      options,
    );
    execFileSync(quality, [], options);
    await mkdir(dirname(output), { recursive: true });
    execFileSync(
      compiler,
      windowsAudioCompileArguments(join(source, 'windows.cpp'), output, join(temporary, 'audio.obj')),
      options,
    );
    process.stdout.write(
      probeWindowsAudio(output)
        ? 'GUL_WINDOWS_AUDIO_CAPABILITY_SUPPORTED\n'
        : 'GUL_WINDOWS_AUDIO_CAPABILITY_UNAVAILABLE\n',
    );
    const integration = join(temporary, 'audio-integration.exe');
    execFileSync(
      compiler,
      windowsAudioCompileArguments(
        join(source, 'integration.cpp'),
        integration,
        join(temporary, 'integration.obj'),
      ),
      options,
    );
    try {
      execFileSync(integration, [output], { ...options, timeout: 20000 });
    } catch (error) {
      if (error.status !== 77) throw error;
      process.stdout.write('Native Windows PCM proof skipped: runner has no render endpoint.\n');
    }
    return output;
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}
