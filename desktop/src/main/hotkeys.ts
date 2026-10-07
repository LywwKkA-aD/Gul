import { spawn, type SpawnOptions } from 'node:child_process';
import type { EventEmitter } from 'node:events';
import { isAbsolute } from 'node:path';
import type { Readable, Writable } from 'node:stream';

const unavailable = () => new Error('GUL_SHORTCUT_UNAVAILABLE');
export function parseHoldAccelerator(accelerator: string): {
  readonly virtualKey: number;
  readonly modifiers: number;
} {
  if (
    typeof accelerator !== 'string' ||
    !accelerator ||
    accelerator.length > 96 ||
    /[\u0000-\u0020\u007f]/u.test(accelerator)
  )
    throw unavailable();
  const parts = accelerator.split('+');
  const key = parts.at(-1)!.toLowerCase();
  let modifiers = 0;
  const names: Readonly<Record<string, number>> = {
    ctrl: 1,
    control: 1,
    commandorcontrol: 1,
    cmdorctrl: 1,
    alt: 2,
    shift: 4,
    super: 8,
    meta: 8,
    command: 8,
    cmd: 8,
  };
  for (const part of parts.slice(0, -1)) {
    const modifier = Object.hasOwn(names, part.toLowerCase()) ? names[part.toLowerCase()] : 0;
    if (!modifier || modifiers & modifier) throw unavailable();
    modifiers |= modifier;
  }
  const special: Readonly<Record<string, number>> = {
    backspace: 8,
    tab: 9,
    enter: 13,
    return: 13,
    capslock: 20,
    escape: 27,
    esc: 27,
    space: 32,
    pageup: 33,
    pagedown: 34,
    end: 35,
    home: 36,
    left: 37,
    up: 38,
    right: 39,
    down: 40,
    insert: 45,
    delete: 46,
  };
  let virtualKey = Object.hasOwn(special, key) ? special[key] : 0;
  if (/^[a-z0-9]$/u.test(key)) virtualKey = key.toUpperCase().charCodeAt(0);
  else if (/^f(?:[1-9]|1\d|2[0-4])$/u.test(key)) virtualKey = 0x6f + Number(key.slice(1));
  else if (/^num[0-9]$/u.test(key)) virtualKey = 0x60 + Number(key.slice(3));
  if (!virtualKey || (virtualKey === 46 && (modifiers & 3) === 3) || (virtualKey === 0x4c && modifiers & 8))
    throw unavailable();
  return Object.freeze({ virtualKey, modifiers });
}

/** XDG shortcuts use base-layer xkbcommon keysyms, not Electron accelerator aliases. */
export function portalTrigger(accelerator: string): string {
  const { virtualKey: key, modifiers } = parseHoldAccelerator(accelerator);
  const special: Readonly<Record<number, string>> = {
    8: 'BackSpace',
    9: 'Tab',
    13: 'Return',
    20: 'Caps_Lock',
    27: 'Escape',
    32: 'space',
    33: 'Prior',
    34: 'Next',
    35: 'End',
    36: 'Home',
    37: 'Left',
    38: 'Up',
    39: 'Right',
    40: 'Down',
    45: 'Insert',
    46: 'Delete',
  };
  const name =
    special[key] ??
    (key >= 0x70 && key <= 0x87
      ? `F${key - 0x6f}`
      : key >= 0x60 && key <= 0x69
        ? `KP_${key - 0x60}`
        : String.fromCharCode(key).toLowerCase());
  return [...['CTRL', 'ALT', 'SHIFT', 'LOGO'].filter((_, index) => modifiers & (1 << index)), name].join('+');
}
export interface HotkeyProcess extends Pick<EventEmitter, 'on' | 'once'> {
  readonly stdin: Writable;
  readonly stdout: Readable;
  readonly exitCode: number | null;
  kill(signal?: NodeJS.Signals): boolean;
}
interface Options {
  readonly executable: string;
  readonly platform?: string;
  readonly parentPID?: number;
  readonly emit: (pressed: boolean) => void;
  readonly onFailure?: () => void;
  readonly startupTimeoutMs?: number;
  readonly spawn?: (file: string, args: readonly string[], options: SpawnOptions) => HotkeyProcess;
}
interface Active {
  readonly child: HotkeyProcess;
  readonly resolve: () => void;
  readonly reject: (error: Error) => void;
  readonly timeout: ReturnType<typeof setTimeout>;
  ready: boolean;
  buffer: string;
}

/** Bundled helpers publish only up/down. They never return keys, paths or process output. */
export class NativeHoldHotkey {
  private readonly options: Options;
  private active?: Active;
  private generation = 0;
  private pressed = false;
  private readonly closing = new WeakMap<HotkeyProcess, Promise<void>>();
  constructor(options: Options) {
    this.options = options;
  }
  available(): boolean {
    return this.active?.ready === true;
  }
  async register(accelerator: string): Promise<void> {
    const generation = ++this.generation;
    await this.stopActive();
    if (generation !== this.generation) throw unavailable();
    const binding = parseHoldAccelerator(accelerator);
    const parent = this.options.parentPID ?? process.pid;
    const platform = this.options.platform ?? process.platform;
    if (
      !['win32', 'linux'].includes(platform) ||
      !isAbsolute(this.options.executable) ||
      !Number.isSafeInteger(parent) ||
      parent <= 0 ||
      parent > 0xffffffff
    )
      throw unavailable();
    const options: SpawnOptions = {
      stdio: ['pipe', 'pipe', 'ignore'],
      windowsHide: true,
      shell: false,
      env: Object.fromEntries(
        (platform === 'win32'
          ? ['SystemRoot', 'WINDIR']
          : [
              'DBUS_SESSION_BUS_ADDRESS',
              'XDG_RUNTIME_DIR',
              'DISPLAY',
              'WAYLAND_DISPLAY',
              'XDG_CURRENT_DESKTOP',
              'HOME',
              'LANG',
            ]
        ).flatMap((name) => (process.env[name] ? [[name, process.env[name]]] : [])),
      ),
    };
    const args =
      platform === 'win32'
        ? [String(binding.virtualKey), String(binding.modifiers), String(parent)]
        : [portalTrigger(accelerator), String(parent)];
    let child: HotkeyProcess;
    try {
      if (this.options.spawn) child = this.options.spawn(this.options.executable, args, options);
      else {
        const process = spawn(this.options.executable, args, options);
        if (!process.stdin || !process.stdout) {
          process.kill();
          throw unavailable();
        }
        child = process as HotkeyProcess;
      }
    } catch {
      throw unavailable();
    }
    await new Promise<void>((resolve, reject) => {
      const deadline = platform === 'linux' ? 60000 : 3000;
      const milliseconds = Math.max(1, Math.min(deadline, this.options.startupTimeoutMs ?? deadline));
      const active: Active = {
        child,
        resolve,
        reject,
        ready: false,
        buffer: '',
        timeout: setTimeout(() => this.fail(active), milliseconds),
      };
      this.active = active;
      child.stdout.on('data', (bytes: Buffer) => this.receive(active, bytes));
      child.stdout.on('error', () => this.fail(active));
      child.stdin.on('error', () => this.fail(active));
      child.on('error', () => this.fail(active));
      child.once('exit', () => this.fail(active));
    });
  }
  async dispose(): Promise<void> {
    ++this.generation;
    await this.stopActive();
  }
  private emit(pressed: boolean): void {
    this.pressed = pressed;
    try {
      this.options.emit(pressed);
    } catch {
      /* A closing window cannot receive an event. */
    }
  }
  private receive(active: Active, bytes: Buffer): void {
    if (this.active !== active) return;
    if (!Buffer.isBuffer(bytes) || bytes.length > 4096) {
      this.fail(active);
      return;
    }
    const lines = (active.buffer + bytes.toString('utf8')).split('\n');
    active.buffer = lines.pop()!;
    if (active.buffer.length > 8) {
      this.fail(active);
      return;
    }
    for (const input of lines) {
      const line = input.replace(/\r$/u, '');
      if (!['up', 'down'].includes(line) || (!active.ready && line !== 'up')) {
        this.fail(active);
        return;
      }
      if (!active.ready) {
        active.ready = true;
        clearTimeout(active.timeout);
        active.resolve();
      }
      const pressed = line === 'down';
      if (pressed !== this.pressed) this.emit(pressed);
    }
  }
  private fail(active: Active): void {
    if (this.active !== active) return;
    this.active = undefined;
    clearTimeout(active.timeout);
    this.emit(false);
    if (!active.ready) active.reject(unavailable());
    else {
      try {
        this.options.onFailure?.();
      } catch {
        /* No exception details cross IPC. */
      }
    }
    void this.closeChild(active.child);
  }
  private async stopActive(): Promise<void> {
    const active = this.active;
    this.active = undefined;
    this.emit(false);
    if (!active) return;
    clearTimeout(active.timeout);
    if (!active.ready) active.reject(unavailable());
    await this.closeChild(active.child);
  }
  private closeChild(child: HotkeyProcess): Promise<void> {
    const current = this.closing.get(child);
    if (current) return current;
    const closed = new Promise<void>((resolve) => {
      if (child.exitCode !== null) {
        resolve();
        return;
      }
      const timer = setTimeout(() => {
        try {
          child.kill();
        } catch {}
        resolve();
      }, 800);
      child.once('exit', () => {
        clearTimeout(timer);
        resolve();
      });
      try {
        child.stdin.end();
      } catch {
        try {
          child.kill();
        } catch {}
        clearTimeout(timer);
        resolve();
      }
    });
    this.closing.set(child, closed);
    return closed;
  }
}
