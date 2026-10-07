import { execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function vcvarsCommand(path) {
  if (
    typeof path !== 'string' ||
    !/^[A-Za-z]:\\/u.test(path) ||
    !/\.bat$/iu.test(path) ||
    /["%!&|<>^\r\n]/u.test(path)
  )
    throw new Error('Invalid MSVC environment script');
  return `"call "${path}" >nul && set"`;
}
export function compileArguments(source, output, object) {
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
    '/DNDEBUG',
    `/Fo${object}`,
    `/Fe${output}`,
    source,
    '/link',
    '/SUBSYSTEM:CONSOLE',
    '/DYNAMICBASE',
    '/NXCOMPAT',
    '/HIGHENTROPYVA',
    'user32.lib',
  ];
}
export function linuxCompileArguments(source, output, gioFlags) {
  return [
    '-std=c++17',
    '-O2',
    '-Wall',
    '-Wextra',
    '-Werror',
    '-fstack-protector-strong',
    '-D_FORTIFY_SOURCE=2',
    '-fPIE',
    '-pie',
    '-Wl,-z,relro,-z,now',
    source,
    '-o',
    output,
    ...gioFlags,
  ];
}
export async function buildLinuxPTT() {
  if (process.platform !== 'linux') return undefined;
  if (process.arch !== 'x64') throw new Error('Linux PTT requires an x64 build host');
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const output = join(root, 'resources', 'ptt', 'linux-x64', 'gul-ptt');
  const flags = execFileSync('pkg-config', ['--cflags', '--libs', 'gio-2.0'], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024,
  })
    .trim()
    .split(/\s+/u);
  await mkdir(dirname(output), { recursive: true });
  execFileSync('g++', linuxCompileArguments(join(root, 'native', 'ptt', 'linux.cpp'), output, flags), {
    stdio: 'inherit',
  });
  return output;
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
  const command = vcvarsCommand(vcvars);
  const cmd = join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'cmd.exe');
  const content = execFileSync(cmd, ['/d', '/s', '/c', command], {
    encoding: 'utf8',
    windowsHide: true,
    windowsVerbatimArguments: true,
    maxBuffer: 1024 * 1024,
  });
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
export async function buildWindowsPTT() {
  if (process.platform !== 'win32') return undefined;
  if (process.arch !== 'x64') throw new Error('Windows PTT requires an x64 build host');
  const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
  const output = join(root, 'resources', 'ptt', 'win32-x64', 'gul-ptt.exe');
  const source = join(root, 'native', 'ptt', 'windows.cpp');
  const temporary = await mkdtemp(join(tmpdir(), 'gul-ptt-build-'));
  try {
    const { compiler, environment } = await compilerEnvironment();
    await mkdir(dirname(output), { recursive: true });
    execFileSync(compiler, compileArguments(source, output, join(temporary, 'windows.obj')), {
      cwd: temporary,
      env: environment,
      stdio: 'inherit',
      windowsHide: true,
    });
    return output;
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const output = process.platform === 'linux' ? await buildLinuxPTT() : await buildWindowsPTT();
    process.stdout.write(
      output ? 'Native PTT helper built.\n' : 'Native PTT helpers are built on Windows or Linux only.\n',
    );
  } catch {
    process.stderr.write('Unable to build native PTT helper.\n');
    process.exitCode = 1;
  }
}
