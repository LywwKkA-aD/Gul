import { app, dialog, shell, safeStorage, type BrowserWindow } from 'electron';
import { join } from 'node:path';
import { SavedServerStore } from './servers.ts';
import { ConnectionManager } from './connections.ts';
import { DiagnosticsJournal, exportDiagnostics } from './diagnostics.ts';
import { checkForUpdate, trustedReleaseURL, type UpdateNotice } from './updates.ts';
import { getCaptureCapabilities } from './capture-capabilities.ts';
import type { SessionAuthority } from './session.ts';
import type { AppInfo, ServerList } from '../shared/contracts.ts';
import { failure } from './validation.ts';

export class AppServices {
  readonly servers: SavedServerStore;
  readonly connections: ConnectionManager;
  readonly journal = new DiagnosticsJournal();
  private update: UpdateNotice | null = null;
  private readonly updateAbort = new AbortController();
  private updateCheck: Promise<unknown> = Promise.resolve();
  constructor(authority: SessionAuthority) {
    this.servers = new SavedServerStore({
      file: join(app.getPath('userData'), 'servers.json'),
      safeStorage,
      platform: process.platform,
    });
    this.connections = new ConnectionManager(authority, this.servers);
  }
  async initialize(): Promise<void> {
    await this.servers.load();
    this.journal.record('app-start');
    if (app.isPackaged)
      this.updateCheck = checkForUpdate({ current: app.getVersion(), signal: this.updateAbort.signal }).then(
        (update) => {
          this.update = update;
        },
      );
  }
  serverList(): ServerList {
    return { servers: this.servers.list(), storage: this.servers.storageStatus() };
  }
  async info(): Promise<AppInfo> {
    await this.updateCheck;
    return {
      version: app.getVersion(),
      update: this.update ? { version: this.update.version, url: this.update.url } : null,
    };
  }
  capabilities() {
    return getCaptureCapabilities();
  }
  async openUpdate(): Promise<void> {
    if (this.update && trustedReleaseURL(this.update.url)) await shell.openExternal(this.update.url);
  }
  record(value: unknown): void {
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw failure('GUL_INPUT_INVALID');
    const { event, metadata } = value as Record<string, unknown>;
    if (typeof event !== 'string' || event.length > 48) throw failure('GUL_INPUT_INVALID');
    this.journal.record(event, metadata);
  }
  async diagnostics(window: BrowserWindow): Promise<boolean> {
    const choice = await dialog.showSaveDialog(window, {
      title: 'Сохранить диагностику Gul',
      defaultPath: `Gul-diagnostics-${app.getVersion()}.zip`,
      filters: [{ name: 'ZIP archive', extensions: ['zip'] }],
    });
    if (choice.canceled || !choice.filePath) return false;
    await exportDiagnostics({
      destination: choice.filePath,
      version: app.getVersion(),
      journal: this.journal,
      electronVersion: process.versions.electron,
      chromiumVersion: process.versions.chrome,
    });
    return true;
  }
  async close(): Promise<void> {
    this.updateAbort.abort();
    await this.connections.disconnect();
  }
}
