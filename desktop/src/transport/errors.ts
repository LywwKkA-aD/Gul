export type GatewayCode = 'profile' | 'transport' | 'authentication' | 'stale' | 'server' | 'closed';

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
    };
    super(messages[code]);
    this.name = 'GatewayError';
    this.code = code;
  }
}

export const failure = () => new GatewayError('transport');
