const messages: Readonly<Record<string, string>> = Object.freeze({
  GUL_MEMBER_KEY_REQUIRED: 'Личный ключ недоступен. Разблокируйте хранилище или загрузите файл ключа заново.',
  GUL_MEMBER_IMPORT_FAILED: 'Файл не является корректным личным ключом Gul.',
  GUL_MEMBER_MISMATCH: 'Личный ключ относится к другому серверу или участнику.',
  GUL_MEMBER_UNSUPPORTED:
    'Этот сервер не поддерживает личные ключи. Обновите сервер или удалите ключ для этого адреса.',
  GUL_INFO_INVALID: 'Сервер вернул некорректные сведения о правах доступа.',
  GUL_OWNER_REQUIRED: 'Управлять каналами может только владелец сервера.',
  GUL_ACCESS_DENIED: 'У вас нет доступа к этому каналу.',
  GUL_CATALOG_CONFLICT: 'Канал изменился. Обновите список и повторите действие.',
  GUL_CHANNEL_BUSY: 'В канале есть участники или активные медиаподключения. Сначала освободите канал.',
  GUL_UPGRADE_REQUIRED: 'Для этого сервера нужна новая версия Gul.',
  GUL_CLEANUP_PENDING: 'Сервер завершает медиаподключения. Повторите действие чуть позже.',
  GUL_SERVER_STORAGE_UNAVAILABLE: 'Сервер не смог сохранить изменения. Повторите действие позже.',
  GUL_MANAGEMENT_FAILED: 'Не удалось изменить каналы или получить права доступа.',
  GUL_REDEEM_FAILED: 'Не удалось принять приглашение. Проверьте код, адрес и пароль сервера.',
  GUL_INPUT_INVALID: 'Проверьте введённые данные.',
  GUL_CONNECT_FAILED: 'Не удалось подключиться через VLESS REALITY. Проверьте адрес и пароль.',
  GUL_GRANT_INVALID: 'Сервер выдал некорректное разрешение на медиаподключение.',
  GUL_STATE_INVALID: 'Сервер вернул некорректное состояние канала.',
  GUL_SESSION_STALE: 'Канал изменился. Повторите действие.',
  GUL_NOT_CONNECTED: 'Сначала подключитесь к серверу.',
  GUL_STATE_FAILED: 'Не удалось обновить состояние канала.',
  GUL_CHANNEL_FAILED: 'Не удалось перейти в канал. Подключитесь к серверу заново.',
  GUL_AUDIO_FAILED: 'Не удалось изменить состояние микрофона.',
  GUL_SCREEN_FAILED: 'Не удалось подключить демонстрацию к каналу.',
  GUL_IPC_DENIED: 'Действие недоступно в этом окне.',
  GUL_SHORTCUT_UNAVAILABLE: 'Не удалось зарегистрировать сочетание клавиш. Выберите другое.',
  GUL_SAVED_PASSWORD_REQUIRED: 'Введите пароль заново: сохранённый пароль недоступен.',
  GUL_STORAGE_WRITE_FAILED:
    'Не удалось изменить сохранённый профиль. Проверьте доступ к папке данных приложения.',
  GUL_SCREEN_AUDIO_UNAVAILABLE:
    'Не удалось включить звук демонстрации без голосов Gul. Проверьте работу PipeWire/PulseAudio.',
  GUL_DIAGNOSTICS_FAILED: 'Не удалось сохранить диагностический архив.',
});
/** Electron wraps invoke failures; arbitrary exception strings never reach the UI. */
export function publicError(error: unknown): Error {
  const code = error instanceof Error ? error.message.match(/\bGUL_[A-Z_]+\b/u)?.[0] : undefined;
  return new Error(
    code && messages[code] ? messages[code] : 'Не удалось выполнить действие. Повторите попытку.',
  );
}
