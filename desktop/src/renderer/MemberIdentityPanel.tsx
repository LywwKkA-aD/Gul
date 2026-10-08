import { useEffect, useRef, useState } from 'react';
import type { DesktopAPI, ConnectInput } from '../shared/contracts.ts';
import type { MemberCredentialInfo } from '../shared/management.ts';
import { Dialog } from './Dialog.tsx';

export function MemberIdentityPanel({
  api,
  input,
  busy,
  protectedStorage,
  onError,
}: {
  api: DesktopAPI;
  input: ConnectInput;
  busy: boolean;
  protectedStorage: boolean;
  onError: (message: string) => void;
}) {
  const [info, setInfo] = useState<MemberCredentialInfo | null>(null),
    [remember, setRemember] = useState(false),
    [pending, setPending] = useState(false),
    [redeem, setRedeem] = useState(false);
  const generation = useRef(0),
    current = useRef({ address: input.address, busy });
  current.current = { address: input.address, busy };
  useEffect(() => {
    const revision = ++generation.current;
    setInfo(null);
    setRedeem(false);
    setRemember(false);
    setPending(false);
    if (!input.address.trim()) return;
    void Promise.resolve()
      .then(() => api.memberCredential(input.address))
      .then((value) => {
        if (revision === generation.current) {
          setInfo(value);
          setRemember(value.rememberIdentity);
        }
      })
      .catch(() => {});
    return () => {
      ++generation.current;
    };
  }, [api, input.address]);
  useEffect(() => {
    if (busy) setRedeem(false);
  }, [busy]);
  const run = async (action: () => Promise<MemberCredentialInfo | null | void>) => {
    if (busy || pending) return;
    const address = input.address,
      revision = generation.current;
    setPending(true);
    try {
      const value = await action();
      if (revision === generation.current && current.current.address === address && !current.current.busy) {
        const next = value ?? (await api.memberCredential(address));
        if (revision === generation.current && !current.current.busy) {
          setInfo(next);
          setRemember(next.rememberIdentity);
        }
      }
    } catch (error) {
      if (revision === generation.current && !current.current.busy)
        onError(error instanceof Error ? error.message : 'Не удалось загрузить личный ключ');
    } finally {
      if (revision === generation.current) setPending(false);
    }
  };
  return (
    <section className="member-identity" aria-label="Личный доступ к серверу">
      <details>
        <summary>Личный доступ к серверу</summary>
        <p className="subtle">
          Для владельца нужен файл личного ключа. Участник получает код приглашения от владельца; общий пароль
          сервера остаётся прежним.
        </p>
        {info?.usable && (
          <p role="status">{info.state === 'saved' ? 'Личный ключ сохранён' : 'Личный ключ загружен'}</p>
        )}
        {info && !info.usable && info.state !== 'none' && (
          <p role="status" className="error">
            Личный ключ недоступен. Разблокируйте хранилище или загрузите файл заново. Подключение как гость
            не заменяет этот ключ.
          </p>
        )}
        {info?.saveError && (
          <p role="status" className="subtle">
            Ключ доступен до закрытия Gul. Защищённое сохранение не удалось.
          </p>
        )}
        <label className="checkbox-control">
          <input
            type="checkbox"
            checked={remember}
            disabled={busy || pending || !protectedStorage}
            onChange={(event) => {
              const rememberIdentity = event.target.checked;
              if (info?.usable)
                void run(() => api.setMemberCredentialConsent({ address: input.address, rememberIdentity }));
              else setRemember(rememberIdentity);
            }}
          />
          Запомнить личный ключ на этом компьютере
        </label>
        <div className="member-actions">
          <button
            type="button"
            className="secondary-button"
            disabled={busy || pending || !input.address.trim()}
            onClick={() =>
              void run(() =>
                api.importMemberCredential({ address: input.address, rememberIdentity: remember }),
              )
            }
          >
            Загрузить личный ключ
          </button>
          <button
            type="button"
            className="secondary-button"
            disabled={busy || pending || !input.address.trim() || !input.username.trim()}
            onClick={() => setRedeem(true)}
          >
            Принять приглашение
          </button>
          {info && info.state !== 'none' && (
            <button
              type="button"
              className="secondary-button"
              disabled={busy || pending}
              onClick={() => void run(() => api.clearMemberCredential(input.address))}
            >
              Удалить личный ключ
            </button>
          )}
        </div>
      </details>
      {redeem && (
        <RedeemDialog
          api={api}
          input={input}
          rememberIdentity={remember}
          onClose={() => setRedeem(false)}
          onSuccess={(value) => {
            setInfo(value);
            setRedeem(false);
          }}
        />
      )}
    </section>
  );
}
function RedeemDialog({
  api,
  input,
  rememberIdentity,
  onClose,
  onSuccess,
}: {
  api: DesktopAPI;
  input: ConnectInput;
  rememberIdentity: boolean;
  onClose: () => void;
  onSuccess: (value: MemberCredentialInfo) => void;
}) {
  const [token, setToken] = useState(''),
    [pending, setPending] = useState(false),
    [error, setError] = useState('');
  const alive = useRef(true);
  useEffect(
    () => () => {
      alive.current = false;
    },
    [],
  );
  return (
    <Dialog title="Принять приглашение" onClose={onClose}>
      <h2>Принять приглашение</h2>
      <p className="subtle">
        Введите одноразовый код от владельца. Приглашение действует сутки. Понадобятся адрес, ник и общий
        пароль из формы подключения.
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (pending) return;
          setPending(true);
          setError('');
          void api
            .redeemInvitation({ input, inviteToken: token.trim(), rememberIdentity })
            .then((value) => {
              if (alive.current) onSuccess(value);
            })
            .catch((error) => {
              if (alive.current)
                setError(error instanceof Error ? error.message : 'Не удалось принять приглашение');
            })
            .finally(() => {
              if (alive.current) setPending(false);
            });
        }}
      >
        <label>
          Код приглашения
          <input
            aria-label="Код приглашения"
            type="password"
            autoComplete="off"
            value={token}
            onChange={(event) => setToken(event.target.value)}
            maxLength={43}
            required
            disabled={pending}
          />
        </label>
        <button className="primary" type="submit" disabled={pending || !token.trim()}>
          Принять приглашение
        </button>
      </form>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </Dialog>
  );
}
