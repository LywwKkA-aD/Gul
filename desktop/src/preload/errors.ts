const messages: Readonly<Record<string, string>> = Object.freeze({
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
  GUL_DIAGNOSTICS_FAILED: 'Не удалось сохранить диагностический архив.',
});
/** Electron wraps invoke failures; arbitrary exception strings never reach the UI. */
export function publicError(error: unknown): Error {
  const code = error instanceof Error ? error.message.match(/\bGUL_[A-Z_]+\b/u)?.[0] : undefined;
  return new Error(
    code && messages[code] ? messages[code] : 'Не удалось выполнить действие. Повторите попытку.',
  );
}
