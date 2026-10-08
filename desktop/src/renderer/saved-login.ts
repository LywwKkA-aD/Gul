import type { SavedServerInfo, ServerList, PasswordStorageRecovery } from '../shared/contracts.ts';

export function selectedSavedServer(list: ServerList, address: string): SavedServerInfo | undefined {
  return list.servers.find((server) => server.address === address.trim());
}

export function savedPasswordMessage(server: SavedServerInfo | undefined): string {
  if (server?.passwordStatus === 'locked')
    return 'Системное хранилище заблокировано. Нажмите «Разблокировать хранилище»; пароль хранилища вводится в системном окне Ubuntu, а не в Gul.';
  if (server?.passwordStatus === 'unreadable')
    return 'Не удалось расшифровать сохранённый пароль. Полностью закройте Gul и запустите снова. Если пароль всё ещё недоступен, введите его вручную и сохраните после входа. Зашифрованная запись не удалена.';
  if (server?.passwordStatus === 'save-failed')
    return 'Не удалось сохранить пароль. Введите его снова; после успешного входа приложение повторит сохранение.';
  return '';
}

export function passwordStorageRecoveryMessage(result: PasswordStorageRecovery): string {
  if (result.state === 'unlocked')
    return result.restartRequired
      ? 'Хранилище разблокировано. Полностью закройте Gul и запустите снова, чтобы восстановить доступ к сохранённому паролю.'
      : 'Хранилище разблокировано. Список сохранённых паролей обновлён.';
  if (result.state === 'cancelled')
    return 'Разблокирование отменено. Сохранённый пароль не изменён; можно повторить попытку или ввести пароль сервера вручную.';
  if (result.state === 'missing')
    return 'Системное хранилище не настроено. Откройте «Пароли и ключи», создайте защищённую связку паролей и назначьте её связкой по умолчанию. Затем полностью перезапустите Gul и сохраните пароль после входа.';
  return 'Не удалось открыть системный запрос разблокирования. Откройте приложение Ubuntu «Пароли и ключи», разблокируйте связку «Вход» (Login), затем полностью перезапустите Gul.';
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
