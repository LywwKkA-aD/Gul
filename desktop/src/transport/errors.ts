export type GatewayCode =
  | 'profile'
  | 'transport'
  | 'authentication'
  | 'stale'
  | 'server'
  | 'closed'
  | 'not-found'
  | 'owner-required'
  | 'access-denied'
  | 'channel-busy'
  | 'upgrade-required'
  | 'cleanup-pending'
  | 'storage-unavailable';

/** Never attach SDK/socket errors: they may contain credentials or private URLs. */
export class GatewayError extends Error {
  readonly code: GatewayCode;
  constructor(code: GatewayCode) {
    const messages: Record<GatewayCode, string> = {
      profile: 'Некорректный профиль REALITY',
      transport: 'Не удалось подключиться через REALITY',
      authentication: 'Сервер не принял имя или пароль',
      stale: 'Канал изменился; повторите действие',
      server: 'Сервер не ответил корректно',
      closed: 'Подключение закрыто',
      'not-found': 'Сервер не поддерживает это действие',
      'owner-required': 'Действие доступно только владельцу сервера',
      'access-denied': 'Нет доступа к этому каналу',
      'channel-busy': 'Канал занят; удаление недоступно',
      'upgrade-required': 'Обновите Gul для подключения к этому серверу',
      'cleanup-pending': 'Сервер завершает предыдущие медиаподключения; повторите позже',
      'storage-unavailable': 'Сервер не смог сохранить изменения',
    };
    super(messages[code]);
    this.name = 'GatewayError';
    this.code = code;
  }
}

export const failure = () => new GatewayError('transport');
