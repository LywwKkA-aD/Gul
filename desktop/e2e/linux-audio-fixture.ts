import { spawn, execFile, type ChildProcess } from 'node:child_process';
import { mkdtemp, mkdir, open, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

interface AudioFixture {
  readonly environments: readonly Readonly<Record<string, string>>[];
  readonly close: () => Promise<void>;
}
const pause = (duration: number) => new Promise((resolve) => setTimeout(resolve, duration));
/** Only the daemon loses desktop bus discovery; Electron retains its real portal/keyring bus. */
export function daemonEnvironment(
  environment: Readonly<Record<string, string>>,
  directory: string,
): Readonly<Record<string, string>> {
  return Object.freeze({
    ...environment,
    XDG_CONFIG_HOME: join(directory, 'config'),
    XDG_DATA_HOME: join(directory, 'data'),
    XDG_RUNTIME_DIR: join(directory, 'runtime'),
    DBUS_SESSION_BUS_ADDRESS: `unix:path=${join(directory, 'disabled-bus')}`,
  });
}
export async function stopFixtureProcess(child: ChildProcess): Promise<void> {
  if (child.pid === undefined || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => {
      child.kill('SIGKILL');
    }, 2000);
    child.once('exit', () => {
      clearTimeout(timer);
      resolve();
    });
    child.kill('SIGTERM');
  });
}
function command(
  file: string,
  args: readonly string[],
  environment: Readonly<Record<string, string>>,
): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(
      file,
      [...args],
      { env: { ...process.env, ...environment }, timeout: 3000, maxBuffer: 64 * 1024 },
      (error) => (error ? reject(new Error('Isolated audio fixture unavailable.')) : resolve()),
    );
  });
}

/** One private local audio server per peer prevents two test Gul trees recapturing one another.
 * paplay is foreign to each Electron tree; the real helper must include it by PID, not name.
 */
export async function isolatedGameAudio(): Promise<AudioFixture> {
  if (process.platform !== 'linux') return { environments: [{}, {}], close: async () => {} };
  const root = await mkdtemp(join(tmpdir(), 'gul-external-game-'));
  const file = join(root, 'stereo.wav');
  const duration = 240;
  const rate = 48000;
  const frames = Buffer.alloc(rate * 4);
  for (let frame = 0; frame < rate; frame++) {
    frames.writeInt16LE(Math.round(3276 * Math.sin((2 * Math.PI * 440 * frame) / rate)), frame * 4);
    frames.writeInt16LE(Math.round(3276 * Math.sin((2 * Math.PI * 880 * frame) / rate)), frame * 4 + 2);
  }
  const header = Buffer.alloc(44);
  header.write('RIFF', 0);
  header.writeUInt32LE(36 + frames.length * duration, 4);
  header.write('WAVEfmt ', 8);
  header.writeUInt32LE(16, 16);
  header.writeUInt16LE(1, 20);
  header.writeUInt16LE(2, 22);
  header.writeUInt32LE(rate, 24);
  header.writeUInt32LE(rate * 4, 28);
  header.writeUInt16LE(4, 32);
  header.writeUInt16LE(16, 34);
  header.write('data', 36);
  header.writeUInt32LE(frames.length * duration, 40);
  const children: ChildProcess[] = [];
  let closing: Promise<void> | undefined;
  const close = () => {
    closing ??= (async () => {
      try {
        await Promise.all([...children].reverse().map(stopFixtureProcess));
      } finally {
        await rm(root, { recursive: true, force: true });
      }
    })();
    return closing;
  };
  try {
    const handle = await open(file, 'wx', 0o600);
    try {
      await handle.write(header);
      for (let second = 0; second < duration; second++) await handle.write(frames);
    } finally {
      await handle.close();
    }
    const environments: Readonly<Record<string, string>>[] = [];
    const launch = async (
      file: string,
      args: readonly string[],
      environment: Readonly<Record<string, string>>,
    ) => {
      const child = spawn(file, [...args], {
        stdio: 'ignore',
        shell: false,
        env: { ...process.env, ...environment },
      });
      children.push(child);
      await new Promise<void>((resolve, reject) => {
        child.once('spawn', resolve);
        child.once('error', () => reject(new Error('Isolated audio fixture unavailable.')));
      });
      return child;
    };
    for (let peer = 0; peer < 2; peer++) {
      const directory = join(root, `peer-${peer}`);
      const runtime = join(directory, 'runtime');
      const config = join(directory, 'config');
      const data = join(directory, 'data');
      await Promise.all([runtime, config, data].map((path) => mkdir(path, { recursive: true, mode: 0o700 })));
      const endpoint = `unix:${join(directory, 'native')}`;
      const environment = { PULSE_SERVER: endpoint, PULSE_RUNTIME_PATH: runtime };
      environments.push(environment);
      const sink = `gul_test_sink_${peer}`;
      const microphone = `gul_test_microphone_${peer}`;
      const server = await launch(
        'pulseaudio',
        [
          '--daemonize=no',
          '--use-pid-file=no',
          '--log-level=error',
          '--exit-idle-time=-1',
          '-n',
          `--load=module-null-sink sink_name=${sink} rate=48000 channels=2 channel_map=front-left,front-right`,
          `--load=module-remap-source master=${sink}.monitor source_name=${microphone} rate=48000 channels=2 channel_map=front-left,front-right source_properties=device.description=Gul-Test-Microphone-${peer}`,
          `--load=module-native-protocol-unix socket=${endpoint.slice(5)} auth-anonymous=1`,
        ],
        daemonEnvironment(environment, directory),
      );
      const deadline = Date.now() + 5000;
      while (
        !(await stat(endpoint.slice(5)).then(
          (info) => info.isSocket(),
          () => false,
        ))
      ) {
        if (Date.now() >= deadline || server.exitCode !== null || server.signalCode !== null)
          throw new Error('Isolated audio fixture unavailable.');
        await pause(50);
      }
      // These defaults belong only to the newly created private server, never the user's server.
      await command('pactl', ['--server', endpoint, 'set-default-sink', sink], environment);
      await command('pactl', ['--server', endpoint, 'set-default-source', microphone], environment);
      const game = await launch(
        'paplay',
        [
          '--server',
          endpoint,
          '--client-name=Gul external test game',
          '--stream-name=Stereo game calibration',
          file,
        ],
        environment,
      );
      await pause(150);
      if (game.exitCode !== null || game.signalCode !== null)
        throw new Error('Isolated game audio unavailable.');
    }
    return { environments: Object.freeze(environments), close };
  } catch (error) {
    await close();
    throw error;
  }
}

/** Linux needs real device enumeration and production display consent, never Chromium fake inputs. */
export const mediaTestArguments = process.platform === 'linux' ? [] : ['--use-fake-device-for-media-stream'];
export const nativeDisplayTestEnvironment: Readonly<Record<string, string>> =
  process.platform === 'linux' ? { GUL_ELECTRON_TEST_CAPTURE_APPROVED: '1' } : {};
