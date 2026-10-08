import { useEffect, useRef, useState } from 'react';
import type { DesktopAPI, MediaSession, BrokerState, ChannelNode } from '../shared/contracts.ts';
import type {
  ManagementContext,
  RegisteredMember,
  ChannelPermissions,
  Invitation,
} from '../shared/management.ts';
import { Dialog } from './Dialog.tsx';
import { flattenChannels } from './ChannelList.tsx';

export function ChannelManagement({
  api,
  session,
  broker,
  busy,
  onState,
}: {
  api: DesktopAPI;
  session: MediaSession;
  broker: BrokerState | null;
  busy: boolean;
  onState: (state: BrokerState) => void;
}) {
  const [dialog, setDialog] = useState<'channels' | 'invite' | null>(null);
  useEffect(() => {
    setDialog(null);
  }, [session, busy]);
  if (!session.serverId || session.member?.role !== 'owner' || broker?.member?.role !== 'owner') return null;
  const context = { epoch: session.epoch, serverId: session.serverId };
  return (
    <>
      <div className="channel-management-actions">
        <button
          type="button"
          className="secondary-button"
          disabled={busy}
          onClick={() => setDialog('channels')}
        >
          Управление каналами
        </button>
        <button
          type="button"
          className="secondary-button"
          disabled={busy}
          onClick={() => setDialog('invite')}
        >
          Пригласить участника
        </button>
      </div>
      {dialog === 'channels' && (
        <ChannelManagerDialog
          api={api}
          context={context}
          state={broker}
          ownerId={session.member.id!}
          onState={onState}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'invite' && (
        <InvitationDialog api={api} context={context} onClose={() => setDialog(null)} />
      )}
    </>
  );
}
function ChannelManagerDialog({
  api,
  context,
  state,
  ownerId,
  onState,
  onClose,
}: {
  api: DesktopAPI;
  context: ManagementContext;
  state: BrokerState;
  ownerId: string;
  onState: (state: BrokerState) => void;
  onClose: () => void;
}) {
  const [members, setMembers] = useState<readonly RegisteredMember[]>([]),
    [selected, setSelected] = useState(1),
    [editing, setEditing] = useState<{ id: number | null; version: number } | null>(null),
    [name, setName] = useState(''),
    [access, setAccess] = useState<'open' | 'restricted'>('open'),
    [allowed, setAllowed] = useState<readonly string[]>([]),
    [pending, setPending] = useState(false),
    [error, setError] = useState(''),
    [deleting, setDeleting] = useState(false);
  const alive = useRef(true),
    request = useRef(0);
  useEffect(
    () => () => {
      alive.current = false;
      ++request.current;
    },
    [],
  );
  useEffect(() => {
    void api
      .members(context)
      .then((value) => {
        if (alive.current) setMembers(value.members);
      })
      .catch((error) => {
        if (alive.current)
          setError(error instanceof Error ? error.message : 'Не удалось получить участников');
      });
  }, [api, context.epoch, context.serverId]);
  const channels = flattenChannels(state.tree),
    channel = channels.find((value) => value.id === selected),
    editChannel = editing?.id === null ? undefined : channels.find((value) => value.id === editing?.id);
  const run = async (action: () => Promise<void>) => {
    if (pending) return;
    setPending(true);
    setError('');
    const revision = ++request.current;
    try {
      await action();
    } catch (error) {
      if (alive.current && revision === request.current) {
        setError(error instanceof Error ? error.message : 'Не удалось изменить канал');
        // A persisted ACL may fail media cleanup. Refresh authoritative state rather than invent a rollback.
        void api
          .state()
          .then((next) => {
            if (alive.current && revision === request.current && next) onState(next);
          })
          .catch(() => {});
      }
    } finally {
      if (alive.current && revision === request.current) setPending(false);
    }
  };
  const begin = (node?: ChannelNode) => {
    setDeleting(false);
    if (!node) {
      setEditing({ id: null, version: 0 });
      setName('');
      setAccess('open');
      setAllowed([]);
      return;
    }
    void run(async () => {
      const policy: ChannelPermissions = await api.channelPermissions({ ...context, channelId: node.id });
      if (!alive.current) return;
      setEditing({ id: node.id, version: policy.version });
      setName(node.name);
      setAccess(policy.access);
      setAllowed(policy.allowedMemberIds);
    });
  };
  const commit = () =>
    void run(async () => {
      if (!editing) return;
      const policy = { access, allowedMemberIds: access === 'restricted' ? allowed : [] };
      const next =
        editing.id === null
          ? await api.createChannel({ ...context, ...policy, name, catalogVersion: state.catalogVersion! })
          : await api.updateChannel({
              ...context,
              ...policy,
              name,
              channelId: editing.id,
              version: editing.version,
            });
      if (alive.current) {
        onState(next);
        setEditing(null);
        setDeleting(false);
      }
    });
  return (
    <Dialog title="Управление каналами" className="channel-manager-dialog" onClose={onClose}>
      <h2>Управление каналами</h2>
      <p className="subtle">
        Только владелец изменяет каналы. В закрытый канал входят выбранные участники; владелец имеет доступ
        всегда.
      </p>
      <label>
        Канал
        <select
          aria-label="Редактируемый канал"
          value={selected}
          disabled={pending}
          onChange={(event) => {
            setSelected(Number(event.target.value));
            setEditing(null);
            setDeleting(false);
          }}
        >
          {channels.map((value) => (
            <option key={value.id} value={value.id}>
              {value.name}
            </option>
          ))}
        </select>
      </label>
      <div className="member-actions">
        <button
          className="secondary-button"
          disabled={pending || channels.length >= 64}
          onClick={() => begin()}
        >
          Новый канал
        </button>
        <button className="secondary-button" disabled={pending || !channel} onClick={() => begin(channel)}>
          Редактировать
        </button>
        <button
          className="secondary-button danger"
          disabled={pending || !channel || channel.id < 2 || !!channel.users?.length}
          onClick={() => {
            setEditing(null);
            setDeleting(true);
          }}
        >
          Удалить канал
        </button>
      </div>
      {deleting && channel && (
        <div className="notice">
          <p>
            Удалить канал «{channel.name}»? Удаление возможно только без участников и активных
            медиаподключений.
          </p>
          <button
            className="secondary-button danger"
            disabled={pending}
            onClick={() =>
              void run(async () => {
                const next = await api.deleteChannel({
                  ...context,
                  channelId: channel.id,
                  version: channel.version!,
                });
                if (alive.current) {
                  onState(next);
                  setDeleting(false);
                  setSelected(1);
                }
              })
            }
          >
            Подтвердить удаление
          </button>
        </div>
      )}
      {editing && (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            commit();
          }}
        >
          <label>
            Название канала
            <input
              aria-label="Название канала"
              value={name}
              maxLength={64}
              required
              disabled={pending}
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <label>
            Доступ
            <select
              aria-label="Доступ к каналу"
              value={access}
              disabled={pending || (editChannel?.id ?? 2) < 2}
              onChange={(event) => setAccess(event.target.value as 'open' | 'restricted')}
            >
              <option value="open">Открытый — все с паролем сервера</option>
              <option value="restricted">Закрытый — выбранные участники</option>
            </select>
          </label>
          {access === 'restricted' && (
            <fieldset className="channel-member-list">
              <legend>Кто может входить</legend>
              {members.map((member) => (
                <label className="checkbox-control" key={member.id}>
                  <input
                    type="checkbox"
                    checked={member.id === ownerId || allowed.includes(member.id)}
                    disabled={
                      pending ||
                      member.id === ownerId ||
                      (member.revoked && !allowed.includes(member.id)) ||
                      (!allowed.includes(member.id) && allowed.length >= 64)
                    }
                    onChange={(event) =>
                      setAllowed((previous) =>
                        event.target.checked
                          ? [...previous, member.id]
                          : previous.filter((id) => id !== member.id),
                      )
                    }
                  />
                  {member.name}
                  {member.id === ownerId ? ' · владелец' : member.revoked ? ' · доступ отозван' : ''}
                </label>
              ))}
              {members.length === 0 && <p className="subtle">Участники ещё не загружены.</p>}
            </fieldset>
          )}
          <button type="submit" className="primary" disabled={pending || !name.trim()}>
            {editing.id === null ? 'Создать канал' : 'Сохранить канал'}
          </button>
        </form>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </Dialog>
  );
}
function InvitationDialog({
  api,
  context,
  onClose,
}: {
  api: DesktopAPI;
  context: ManagementContext;
  onClose: () => void;
}) {
  const [value, setValue] = useState<Invitation | null>(null),
    [pending, setPending] = useState(false),
    [error, setError] = useState('');
  const alive = useRef(true),
    field = useRef<HTMLInputElement>(null);
  useEffect(
    () => () => {
      alive.current = false;
    },
    [],
  );
  return (
    <Dialog title="Приглашение участника" onClose={onClose}>
      <h2>Приглашение участника</h2>
      <p className="subtle">
        Код используется один раз и действует сутки. Передайте его другу вместе с адресом и общим паролем
        сервера.
      </p>
      {!value && (
        <button
          className="primary"
          disabled={pending}
          onClick={() => {
            setPending(true);
            void api
              .createInvitation(context)
              .then((value) => {
                if (alive.current) setValue(value);
              })
              .catch((error) => {
                if (alive.current)
                  setError(error instanceof Error ? error.message : 'Не удалось создать приглашение');
              })
              .finally(() => {
                if (alive.current) setPending(false);
              });
          }}
        >
          Создать приглашение
        </button>
      )}
      {value && (
        <>
          <label>
            Код приглашения
            <input
              ref={field}
              aria-label="Код приглашения"
              value={value.inviteToken}
              readOnly
              autoComplete="off"
              spellCheck={false}
            />
          </label>
          <p className="subtle">
            Действует до {new Date(value.expiresAtUnixSeconds * 1000).toLocaleString('ru-RU')}.
          </p>
          <button
            className="secondary-button"
            onClick={() => {
              field.current?.focus();
              field.current?.select();
            }}
          >
            Выделить код
          </button>
          <p className="subtle">Скопируйте выделенный код обычным сочетанием клавиш.</p>
        </>
      )}
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </Dialog>
  );
}
