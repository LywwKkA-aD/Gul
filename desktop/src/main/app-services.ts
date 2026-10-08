import { app, dialog, shell, safeStorage, type BrowserWindow } from 'electron';
import { join } from 'node:path';
import { MemberCredentialStore } from './member-credentials.ts';
import { MemberAccess } from './member-access.ts';
import type { ImportMemberCredential } from '../shared/management.ts';
import { SavedServerStore } from './servers.ts';
import { ConnectionManager } from './connections.ts';
import { DiagnosticsJournal, exportDiagnostics } from './diagnostics.ts';
import { checkForUpdate, trustedReleaseURL, type UpdateNotice } from './updates.ts';
import { getCaptureCapabilities } from './capture-capabilities.ts';
import type { SessionAuthority } from './session.ts';
import type { AppInfo, ServerList } from '../shared/contracts.ts';
import { failure } from './validation.ts';
import { PasswordStorage } from './password-storage.ts';

export class AppServices {
  readonly memberKeys: MemberCredentialStore;
  readonly members: MemberAccess;
  readonly servers: SavedServerStore;
  readonly connections: ConnectionManager;
  readonly passwordStorage: PasswordStorage;
  readonly journal = new DiagnosticsJournal();
  private update: UpdateNotice | null = null;
  private readonly updateAbort = new AbortController();
  private updateCheck: Promise<unknown> = Promise.resolve();
  constructor(authority: SessionAuthority, passwordStoreExecutable: string) {
    this.passwordStorage = new PasswordStorage({
      platform: process.platform,
      executable: passwordStoreExecutable,
      safeStorage,
      applicationName: app.getName(),
    });
    this.servers = new SavedServerStore({
      file: join(app.getPath('userData'), 'servers.json'),
      safeStorage,
      platform: process.platform,
      passwordStorage: () => this.passwordStorage.getSnapshot(),
    });
    this.memberKeys = new MemberCredentialStore({
      file: join(app.getPath('userData'), 'members.json'),
      safeStorage,
      platform: process.platform,
      passwordStorage: () => this.passwordStorage.getSnapshot(),
    });
    this.members = new MemberAccess(authority, this.memberKeys);
    this.connections = new ConnectionManager(authority, this.servers, this.memberKeys);
  }
  async initialize(): Promise<void> {
    await Promise.all([this.servers.load(), this.memberKeys.load()]);
    this.journal.record('app-start');
    if (app.isPackaged)
      this.updateCheck = checkForUpdate({ current: app.getVersion(), signal: this.updateAbort.signal }).then(
        (update) => {
          this.update = update;
        },
      );
  }
  async serverList(): Promise<ServerList> {
    const passwordStorage = await this.passwordStorage.status();
    return {
      servers: this.servers.list(),
      storage: this.servers.storageStatus(),
      lastSave: this.connections.lastSave(),
      passwordStorage,
    };
  }
  importMemberCredential(window: BrowserWindow, value: ImportMemberCredential) {
    return this.members.import(value, async () => {
      const choice = await dialog.showOpenDialog(window, {
        title: 'Загрузить личный ключ Gul',
        filters: [{ name: 'Gul member key', extensions: ['json'] }],
        properties: ['openFile'],
      });
      return choice.canceled ? null : (choice.filePaths[0] ?? null);
    });
  }
  async forgetServer(address: string): Promise<void> {
    await this.members.clear(address);
    if (!(await this.servers.forget(address)).persisted) throw failure('GUL_STORAGE_WRITE_FAILED');
  }
  async info(): Promise<AppInfo> {
    await this.updateCheck;
    return {
      version: app.getVersion(),
      update: this.update ? { version: this.update.version, url: this.update.url } : null,
    };
  }
  capabilities(options?: { readonly linuxExcludedAudio?: boolean; readonly windowsExcludedAudio?: boolean }) {
    return getCaptureCapabilities(options);
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
    await this.passwordStorage.close();
    await this.connections.disconnect();
  }
}
