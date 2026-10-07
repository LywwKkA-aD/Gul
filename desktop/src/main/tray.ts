export interface TrayHandle {
  isDestroyed(): boolean;
  getBounds(): { readonly width: number; readonly height: number };
  destroy(): void;
}
export interface TrayWindow {
  isDestroyed(): boolean;
  isMinimized(): boolean;
  restore(): void;
  show(): void;
  focus(): void;
  hide(): void;
}
interface Options {
  readonly platform: string;
  readonly window: () => TrayWindow | undefined;
  readonly createTray: (actions: {
    readonly show: () => void;
    readonly quit: () => void;
  }) => TrayHandle | undefined;
  readonly cleanup: () => Promise<void>;
  readonly quit: () => void;
}

/** An app may stay hidden only while a native tray can make its window reachable. */
export class TrayLifecycle {
  private readonly options: Options;
  private tray?: TrayHandle;
  private initialized = false;
  private quitting = false;
  private quitPromise?: Promise<void>;
  constructor(options: Options) {
    this.options = options;
  }

  initialize(): boolean {
    if (this.initialized) return this.usable();
    this.initialized = true;
    try {
      this.tray = this.options.createTray({
        show: () => this.showWindow(),
        quit: () => {
          void this.requestQuit();
        },
      });
      if (!this.usable()) this.dispose();
    } catch {
      this.dispose();
    }
    return this.usable();
  }
  usable(): boolean {
    try {
      if (!this.tray || this.tray.isDestroyed()) return false;
      const { width, height } = this.tray.getBounds();
      return Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0;
    } catch {
      return false;
    }
  }
  showWindow(): void {
    if (this.quitting) return;
    const window = this.options.window();
    if (!window || window.isDestroyed()) return;
    if (window.isMinimized()) window.restore();
    window.show();
    window.focus();
  }
  handleClose(event: { preventDefault(): void }): 'hide' | 'quit' | 'close' {
    if (this.quitting) return 'close';
    event.preventDefault();
    const window = this.options.window();
    if (this.options.platform !== 'linux' && this.usable() && window && !window.isDestroyed()) {
      window.hide();
      return 'hide';
    }
    void this.requestQuit();
    return 'quit';
  }
  /** Root also awaits this method from before-quit so external quit follows the same cleanup. */
  requestQuit(): Promise<void> {
    this.quitPromise ??= (async () => {
      this.quitting = true;
      try {
        await this.options.cleanup();
      } catch {
        /* Cleanup exceptions may contain credentials; exit still proceeds. */
      } finally {
        this.dispose();
        this.options.quit();
      }
    })();
    return this.quitPromise;
  }
  dispose(): void {
    const tray = this.tray;
    this.tray = undefined;
    try {
      if (tray && !tray.isDestroyed()) tray.destroy();
    } catch {
      /* A missing native tray must never block app shutdown. */
    }
  }
}
