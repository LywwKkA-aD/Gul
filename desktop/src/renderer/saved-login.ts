import type { SavedServerInfo, ServerList } from '../shared/contracts.ts';

export function selectedSavedServer(list: ServerList, address: string): SavedServerInfo | undefined {
  return list.servers.find((server) => server.address === address.trim());
}

export function savedPasswordMessage(server: SavedServerInfo | undefined): string {
  if (server?.passwordStatus === 'locked')
    return 'Сохранённый пароль недоступен. Разблокируйте системное хранилище паролей и повторите чтение либо введите пароль вручную.';
  if (server?.passwordStatus === 'save-failed')
    return 'Не удалось сохранить пароль. Введите его снова; после успешного входа приложение повторит сохранение.';
  return '';
}

export function passwordSaveNotice(list: ServerList, address: string): string {
  if (list.lastSave?.address !== address.trim()) return '';
  switch (list.lastSave.status) {
    case 'unavailable':
      return 'Пароль не сохранён: системное хранилище недоступно. При следующем запуске потребуется ввести его снова.';
    case 'encrypt-failed':
      return 'Не удалось сохранить пароль в системном хранилище. При следующем запуске может потребоваться ручной ввод.';
    case 'write-failed':
      return 'Не удалось записать настройки подключения. После перезапуска пароль может потребоваться снова.';
    default:
      return '';
  }
}
