import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import type {
  BrokerState,
  DesktopAPI,
  MediaSession,
  UserInfo,
  ServerList,
  CaptureCapabilities,
  AppInfo,
} from '../shared/contracts.ts';
import { MediaController } from './media/controller.ts';
import type { ChatEntry } from './media/model.ts';
import { Icon } from './MediaElements.tsx';
import { GulLogo } from './GulLogo.tsx';
import { CapturePickerHost } from './CapturePickerHost.tsx';
import { MemberIdentityPanel } from './MemberIdentityPanel.tsx';
import { ChannelManagement } from './ChannelManagement.tsx';
import { ChannelList, flattenChannels } from './ChannelList.tsx';
import { ChatPanel } from './ChatPanel.tsx';
import { EphemeralChatHistory } from './chat-history.ts';
import { ScreenPanel } from './ScreenPanel.tsx';
import { SoundCues } from './sound-cues.ts';
import { ConnectPanel } from './ConnectPanel.tsx';
import { ConnectionLifecycle } from './connection-lifecycle.ts';
import { PreferenceUpdateQueue, mergePreferences, type PreferencePatch } from './preference-updates.ts';
import { RangeUpdates, type RangePatch } from './range-updates.ts';
import { presentationSnapshot } from './presentation-snapshot.ts';
import { SettingsDialog } from './SettingsDialog.tsx';
import { ShareStartDialog } from './ShareStartDialog.tsx';
import { screenPreset, type ScreenQuality } from './media/screen-settings.ts';
import { selectedSavedServer, passwordSaveNotice, passwordStorageRecoveryMessage } from './saved-login.ts';
import {
  ParticipantControls,
  ParticipantRow,
  defaultLocalAudio,
  type LocalAudioPreference,
} from './ParticipantControls.tsx';
import {
  readPreferences,
  readSavedString,
  saveConnection,
  savePreferences,
  type Preferences,
} from './preferences.ts';

declare global {
  interface Window {
    gul: DesktopAPI;
  }
}

export function App() {
  const api = window.gul;
  const sessionRef = useRef<MediaSession | null>(null);
  const [media] = useState(
    () =>
      new MediaController({
        screenGrant: () => {
          const session = sessionRef.current;
          if (!session) return Promise.reject(new Error('Нет активного подключения'));
          return api.screen({ channelId: session.channelId, revision: session.revision });
        },
        audioState: (state) => api.audio(state),
      }),
  );
  const [readPresentation] = useState(() => presentationSnapshot(media.getSnapshot));
  const snapshot = useSyncExternalStore(media.subscribe, readPresentation);
  const [cues] = useState(() => new SoundCues());
  const previousMedia = useRef(snapshot);
  const [history] = useState(() => new EphemeralChatHistory());
  const [session, setSession] = useState<MediaSession | null>(null);
  const [broker, setBroker] = useState<BrokerState | null>(null);
  const [address, setAddress] = useState(() => readSavedString('gul.address'));
  const [username, setUsername] = useState(() => readSavedString('gul.username'));
  const [password, setPassword] = useState('');
  const [rememberPassword, setRememberPassword] = useState(false);
  const [savedServers, setSavedServers] = useState<ServerList>({
    servers: [],
    storage: 'unavailable',
    lastSave: null,
  });
  const [storageNotice, setStorageNotice] = useState('');
  const [capabilities, setCapabilities] = useState<CaptureCapabilities | null>(null);
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [settings, setSettings] = useState(false);
  const [shareRequest, setShareRequest] = useState<MediaSession | null>(null);
  const [preferences, setPreferences] = useState(readPreferences);
  const preferenceRef = useRef(preferences);
  const hotkeyRevision = useRef(0);
  const [localAudio, setLocalAudio] = useState<Readonly<Record<string, LocalAudioPreference>>>({});
  const localAudioRef = useRef(localAudio);
  const [selectedUser, setSelectedUser] = useState<UserInfo | null>(null);
  const [chat, setChat] = useState<{ channelId: number; entries: readonly ChatEntry[] } | null>(null);
  const [lifecycle] = useState(() => new ConnectionLifecycle());
  const [preferenceUpdates] = useState(() => new PreferenceUpdateQueue());
  const [rangeUpdates] = useState(() => new RangeUpdates());

  const persistPreferences = (next: Preferences) => {
    preferenceRef.current = Object.freeze(next);
    setPreferences(preferenceRef.current);
    savePreferences(preferenceRef.current);
  };
  useEffect(() => {
    if (
      shareRequest &&
      (session !== shareRequest ||
        busy ||
        snapshot.state !== 'connected' ||
        snapshot.sharing ||
        snapshot.pendingShare)
    )
      setShareRequest(null);
  }, [shareRequest, session, busy, snapshot.state, snapshot.sharing, snapshot.pendingShare]);
  useEffect(() => {
    let active = true;
    void api
      .servers()
      .then((value) => {
        if (active) setSavedServers(value);
      })
      .catch(() => {});
    void api
      .captureCapabilities()
      .then((value) => {
        if (active) setCapabilities(value);
      })
      .catch(() => {});
    void api
      .appInfo()
      .then((value) => {
        if (active) setAppInfo(value);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [api]);
  useEffect(() => {
    setRememberPassword(selectedSavedServer(savedServers, address)?.rememberPassword ?? false);
  }, [savedServers, address]);
  useEffect(() => {
    if (!session) return;
    let active = true;
    let pending = false;
    const update = async () => {
      if (pending) return;
      pending = true;
      try {
        const next = await api.state();
        if (active) {
          if (next === null && sessionRef.current === session) {
            void leave();
            return;
          }
          setBroker(next);
        }
      } catch {
        if (active) setError('Не удалось обновить список участников');
      } finally {
        pending = false;
      }
    };
    void update();
    const timer = setInterval(() => void update(), 1000);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [api, session]);
  useEffect(
    () =>
      api.onPushToTalk((pressed) => {
        if (sessionRef.current && (!pressed || preferenceRef.current.toggleEnabled))
          void media.setAudio({ muted: !pressed, deafened: media.getSnapshot().deafened });
      }),
    [api, media],
  );
  useEffect(() => {
    let active = true;
    const saved = preferenceRef.current;
    const revision = ++hotkeyRevision.current;
    if (saved.toggleEnabled)
      void api.setPushToTalk(saved.shortcut, saved.hotkeyMode).catch(() => {
        if (!active || revision !== hotkeyRevision.current) return;
        void media.setAudio({ muted: true, deafened: media.getSnapshot().deafened });
        persistPreferences({ ...preferenceRef.current, toggleEnabled: false });
        setError('Сохранённая клавиша микрофона недоступна. Выберите другую в настройках.');
      });
    return () => {
      active = false;
    };
  }, [api]);
  useEffect(() => {
    if (!session) return;
    history.write(session.channelId, snapshot.chat);
    setChat({ channelId: session.channelId, entries: history.read(session.channelId) });
  }, [history, session, snapshot.chat]);

  useEffect(() => {
    const before = previousMedia.current;
    previousMedia.current = snapshot;
    const cue =
      before.state !== snapshot.state
        ? snapshot.state === 'connected'
          ? 'connect'
          : snapshot.state === 'reconnecting'
            ? 'reconnect'
            : snapshot.state === 'disconnected'
              ? 'disconnect'
              : undefined
        : before.sharing !== snapshot.sharing
          ? snapshot.sharing
            ? 'screen-start'
            : 'screen-stop'
          : before.deafened !== snapshot.deafened
            ? 'deafen'
            : before.muted !== snapshot.muted
              ? snapshot.muted
                ? 'mute'
                : 'unmute'
              : undefined;
    if (cue) {
      void cues.play(cue, preferences.soundNotifications, snapshot.deafened);
      const event =
        cue === 'reconnect'
          ? 'media-reconnect'
          : cue === 'disconnect'
            ? 'media-disconnected'
            : cue === 'screen-start'
              ? 'capture-start'
              : cue === 'screen-stop'
                ? 'capture-stop'
                : '';
      if (event)
        void api
          .recordDiagnostic(event, {
            state: snapshot.state,
            muted: snapshot.muted,
            deafened: snapshot.deafened,
          })
          .catch(() => {});
    } else if (!preferences.soundNotifications || snapshot.deafened) void cues.play('mute', false, true);
  }, [
    api,
    cues,
    preferences.soundNotifications,
    snapshot.state,
    snapshot.sharing,
    snapshot.muted,
    snapshot.deafened,
  ]);
  useEffect(
    () => () => {
      void cues.close();
      void media.leave();
    },
    [cues, media],
  );

  const run = async (action: () => Promise<void>) => {
    setError('');
    try {
      await action();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Не удалось выполнить действие');
    }
  };
  const leave = async () => {
    const current = lifecycle.invalidate();
    sessionRef.current = null;
    setSession(null);
    setBroker(null);
    setBusy(true);
    setSelectedUser(null);
    setShareRequest(null);
    setPassword('');
    history.reset();
    setChat(null);
    localAudioRef.current = {};
    setLocalAudio({});
    const result = await lifecycle.cleanup(current, media.leave, () => api.disconnect());
    if (!lifecycle.current(current)) return;
    lifecycle.finish(current);
    setBusy(false);
    if (result.failure)
      setError(result.failure instanceof Error ? result.failure.message : 'Не удалось отключиться');
  };
  const enter = async (channel?: number) => {
    if (busy || (channel !== undefined && channel === sessionRef.current?.channelId)) return;
    const current = lifecycle.begin();
    if (current === null) return;
    const previous = sessionRef.current;
    if (previous) history.write(previous.channelId, media.getSnapshot().chat);
    setBusy(true);
    setError('');
    setSelectedUser(null);
    setShareRequest(null);
    try {
      if (!(await lifecycle.step(current, media.leave)).accepted) return;
      const joined = await lifecycle.step(current, () =>
        channel === undefined
          ? savedServers.servers.some((server) => server.address === address.trim() && server.hasPassword) &&
            !password
            ? api.connectSaved(address, username, rememberPassword)
            : api.connect({ address, username, password }, rememberPassword)
          : api.channel(channel),
      );
      if (!joined.accepted) return;
      const next = joined.value;
      if (channel === undefined) history.bind({ server: address.trim(), sessionId: next.sessionId });
      sessionRef.current = next;
      setSession(next);
      const saved = preferenceRef.current;
      const setup = [
        () => media.setVoiceSettings(saved.voice),
        () => media.setDevice('audioinput', saved.audioinput),
        () => media.setDevice('audiooutput', saved.audiooutput),
        ...(saved.toggleEnabled
          ? [() => media.setAudio({ muted: true, deafened: media.getSnapshot().deafened })]
          : []),
        () => media.join(next),
      ];
      for (const step of setup) if (!(await lifecycle.step(current, step)).accepted) return;
      if (media.getSnapshot().state !== 'connected')
        throw new Error(media.getSnapshot().error || 'Не удалось подключить голосовой канал');
      Object.entries(localAudioRef.current).forEach(([identity, preference]) => {
        media.setUserVolume(identity, preference.gain);
        media.setUserMuted(identity, preference.muted);
      });
      saveConnection(address, username);
      setPassword('');
      void api
        .servers()
        .then(setSavedServers)
        .catch(() => {});
    } catch (failure) {
      if (!lifecycle.current(current)) return;
      await lifecycle.cleanup(current, media.leave, () => api.disconnect());
      if (!lifecycle.current(current)) return;
      sessionRef.current = null;
      setSession(null);
      setBroker(null);
      history.reset();
      setChat(null);
      localAudioRef.current = {};
      setLocalAudio({});
      setError(failure instanceof Error ? failure.message : 'Не удалось подключиться');
    } finally {
      if (lifecycle.finish(current)) setBusy(false);
    }
  };
  const changePreferences = (patch: PreferencePatch) => {
    const { screenQuality, ...change } = patch;
    // Quality has no hardware work: preserve new intent while older device or DSP work awaits.
    if (screenQuality !== undefined)
      persistPreferences(mergePreferences(preferenceRef.current, { screenQuality }));
    if (!Object.keys(change).length) return Promise.resolve();
    return preferenceUpdates.run(
      change,
      () => preferenceRef.current,
      async (next, fields) => {
        if (fields.audioinput !== undefined) await media.setDevice('audioinput', next.audioinput);
        if (fields.audiooutput !== undefined) await media.setDevice('audiooutput', next.audiooutput);
        if (fields.voice !== undefined) await media.setVoiceSettings(next.voice);
        if (
          fields.toggleEnabled !== undefined ||
          ((fields.shortcut !== undefined || fields.hotkeyMode !== undefined) && next.toggleEnabled)
        ) {
          const revision = ++hotkeyRevision.current;
          try {
            await api.setPushToTalk(next.toggleEnabled ? next.shortcut : null, next.hotkeyMode);
            if (revision !== hotkeyRevision.current) return;
            if (next.toggleEnabled)
              await media.setAudio({ muted: true, deafened: media.getSnapshot().deafened });
          } catch {
            if (revision !== hotkeyRevision.current) return;
            persistPreferences(mergePreferences(preferenceRef.current, { ...fields, toggleEnabled: false }));
            await media.setAudio({ muted: true, deafened: media.getSnapshot().deafened });
            throw new Error('Не удалось зарегистрировать эту клавишу. Выберите другое сочетание.');
          }
          if (revision !== hotkeyRevision.current) return;
        }
        persistPreferences(mergePreferences(preferenceRef.current, fields));
      },
    );
  };
  const startShare = (quality: ScreenQuality) => {
    const requested = shareRequest;
    setShareRequest(null);
    const current = media.getSnapshot();
    if (
      !requested ||
      sessionRef.current !== requested ||
      busy ||
      current.state !== 'connected' ||
      current.sharing ||
      current.pendingShare
    )
      return;
    persistPreferences(mergePreferences(preferenceRef.current, { screenQuality: quality }));
    // Invoke capture in this click stack; persisting settings must not consume the user gesture.
    void run(() =>
      media.startScreen(
        api.screen({ channelId: requested.channelId, revision: requested.revision }),
        true,
        quality,
      ),
    );
  };
  const changeRanges = (patch: RangePatch) =>
    rangeUpdates.run(patch, (voice) => changePreferences({ voice }));
  const changeLocalAudio = (identity: string, patch: Partial<LocalAudioPreference>) => {
    const previous = localAudioRef.current[identity] ?? defaultLocalAudio;
    const next = { ...previous, ...patch };
    localAudioRef.current = { ...localAudioRef.current, [identity]: next };
    setLocalAudio(localAudioRef.current);
    media.setUserVolume(identity, next.gain);
    media.setUserMuted(identity, next.muted);
  };
  const channels = flattenChannels(broker?.tree);
  const selectedChannel = channels.find((channel) => channel.id === session?.channelId);
  const users: readonly UserInfo[] =
    selectedChannel?.users ??
    snapshot.participants.map((participant) => ({
      name: participant.name,
      key: participant.identity,
      session: Number(participant.identity.slice(6)),
      channelId: session?.channelId ?? 0,
      selfMute: false,
      selfDeaf: false,
      isSelf: participant.identity === session?.identity,
    }));
  const openUser = (user: UserInfo) =>
    user.session === sessionRef.current?.sessionId ? setSettings(true) : setSelectedUser(user);
  const micOff = snapshot.muted || snapshot.deafened;

  return (
    <div className="app">
      <header className="titlebar">
        <span className="wordmark">
          GUL<span className="version">{appInfo?.version ?? 'Electron'}</span>
        </span>
        <div className="window-buttons">
          <button aria-label="Свернуть" onClick={() => void api.minimize()}>
            −
          </button>
          <button aria-label="Развернуть" onClick={() => void api.maximize()}>
            □
          </button>
          <button aria-label="Закрыть" onClick={() => void api.closeWindow()}>
            ×
          </button>
        </div>
      </header>
      {!session ? (
        <ConnectPanel
          address={address}
          username={username}
          password={password}
          remember={rememberPassword}
          saved={savedServers}
          storageNotice={storageNotice}
          busy={busy}
          error={error}
          identityPanel={
            <MemberIdentityPanel
              api={api}
              input={{ address, username, password }}
              busy={busy}
              protectedStorage={savedServers.storage === 'protected'}
              onError={setError}
            />
          }
          onAddress={setAddress}
          onUsername={setUsername}
          onPassword={setPassword}
          onRemember={setRememberPassword}
          onChoose={(server) => {
            setAddress(server.address);
            setUsername(server.username);
            setPassword('');
            setRememberPassword(server.rememberPassword);
          }}
          onForget={(value) =>
            void run(async () => {
              await api.forgetServer(value);
              setSavedServers(await api.servers());
            })
          }
          onRefreshSaved={() => void run(async () => setSavedServers(await api.servers()))}
          onUnlockStorage={() =>
            void run(async () => {
              const result = await api.unlockPasswordStorage();
              setSavedServers(await api.servers());
              setStorageNotice(passwordStorageRecoveryMessage(result));
            })
          }
          onOpenStorage={() =>
            void run(async () => {
              if (!(await api.openPasswordStorage()))
                setError(
                  'Приложение «Пароли и ключи» не установлено. Следуйте инструкции по хранилищу в Gul.',
                );
            })
          }
          onConnect={() => void enter()}
          onCancel={() => void run(leave)}
        />
      ) : (
        <div className="workspace">
          <aside className="sidebar">
            <div className="server-heading">
              <GulLogo className="server-icon" />
              <div>
                <strong>Наш сервер</strong>
                <small className="subtle">{snapshot.state === 'connected' ? 'В сети' : 'Подключение…'}</small>
              </div>
            </div>
            <ChannelManagement
              key={session.epoch}
              api={api}
              session={session}
              broker={broker}
              busy={busy}
              onState={setBroker}
            />
            <div className="section-label">ГОЛОСОВЫЕ КАНАЛЫ</div>
            <ChannelList
              tree={broker?.tree}
              selected={session.channelId}
              selfSession={session.sessionId}
              selfAudio={snapshot}
              busy={busy}
              speakers={snapshot.speakers}
              localAudio={localAudio}
              onChannel={(id) => void enter(id)}
              onUser={openUser}
            />
            <div className="connection-info">
              <span className="status-dot" />
              <div>
                <span>{snapshot.state === 'connected' ? 'Голос подключён' : 'Восстанавливаем связь'}</span>
                <small>
                  {selectedChannel?.name ?? 'Голосовой канал'} ·{' '}
                  {snapshot.pingMs !== null ? `${Math.round(snapshot.pingMs)} мс` : '—'}
                </small>
              </div>
              <button
                className="icon-button leave-button"
                aria-label={busy ? 'Отменить подключение' : 'Отключиться'}
                title={busy ? 'Отменить подключение' : 'Отключиться'}
                onClick={() => void run(leave)}
              >
                <Icon name="leave" />
              </button>
            </div>
            <div className="self-bar">
              <div className="self-identity">
                <div className="avatar">{session.name.slice(0, 1).toUpperCase()}</div>
                <span className="self-name">
                  {session.name}
                  <small>{micOff ? 'Микрофон выключен' : 'В голосовом канале'}</small>
                </span>
              </div>
              <div className="self-controls">
                <button
                  className={`icon-button ${micOff ? 'danger' : ''}`}
                  aria-label={micOff ? 'Включить микрофон' : 'Выключить микрофон'}
                  title={micOff ? 'Включить микрофон' : 'Выключить микрофон'}
                  onClick={() =>
                    void run(() =>
                      media.setAudio(
                        micOff
                          ? { muted: false, deafened: false }
                          : { muted: true, deafened: snapshot.deafened },
                      ),
                    )
                  }
                >
                  <Icon name="mic" off={micOff} />
                </button>
                <button
                  className={`icon-button ${snapshot.deafened ? 'danger' : ''}`}
                  aria-label={snapshot.deafened ? 'Включить звук' : 'Выключить звук'}
                  title={snapshot.deafened ? 'Включить звук' : 'Выключить звук'}
                  onClick={() =>
                    void run(() => media.setAudio({ muted: snapshot.muted, deafened: !snapshot.deafened }))
                  }
                >
                  <Icon name="deaf" off={snapshot.deafened} />
                </button>
                <button
                  className={`icon-button ${snapshot.sharing ? 'active' : ''}`}
                  disabled={
                    snapshot.pendingShare || (!snapshot.sharing && (busy || snapshot.state !== 'connected'))
                  }
                  aria-label={snapshot.sharing ? 'Остановить демонстрацию' : 'Показать экран'}
                  title={
                    snapshot.sharing
                      ? `Остановить демонстрацию${snapshot.screenQuality ? ` · ${screenPreset(snapshot.screenQuality).label}` : ''}`
                      : `Показать экран · ${screenPreset(preferences.screenQuality).label}`
                  }
                  onClick={() => (snapshot.sharing ? void run(media.stopScreen) : setShareRequest(session))}
                >
                  <Icon name="screen" />
                </button>
                <button
                  className={`icon-button ${settings ? 'active' : ''}`}
                  title="Настройки"
                  aria-label="Настройки"
                  onClick={() => setSettings(true)}
                >
                  <Icon name="settings" />
                </button>
              </div>

              {snapshot.sharing && (
                <small className="capture-status">
                  {snapshot.screenQuality ? `${screenPreset(snapshot.screenQuality).label} · ` : ''}
                  {snapshot.screenAudio === 'capturing'
                    ? 'Экран и звук компьютера'
                    : snapshot.screenAudio === 'unavailable'
                      ? 'Экран · звук недоступен'
                      : 'Экран без звука'}
                </small>
              )}
            </div>
          </aside>
          <main className="conversation">
            <div className="channel-heading">
              <Icon name="voice" />
              <h1>{selectedChannel?.name ?? 'Голосовой канал'}</h1>
              <span className="subtle">{users.length} в канале</span>
            </div>
            {(error || snapshot.error || snapshot.warning || passwordSaveNotice(savedServers, address)) && (
              <div className={error || snapshot.error ? 'notice error' : 'notice'} role="alert">
                {error || snapshot.error || snapshot.warning || passwordSaveNotice(savedServers, address)}
              </div>
            )}
            <ScreenPanel
              snapshot={snapshot}
              localAudio={localAudio}
              onWatch={media.watchScreen}
              onVolume={(identity, gain) => changeLocalAudio(identity, { gain })}
              onError={setError}
            />
            <ChatPanel
              key={session.channelId}
              entries={chat?.channelId === session.channelId ? chat.entries : history.read(session.channelId)}
              connected={snapshot.state === 'connected'}
              onSend={media.sendChat}
              onError={setError}
            />
          </main>
          <aside className="members">
            <div className="section-label">В КАНАЛЕ · {users.length}</div>
            {users.map((user) => (
              <ParticipantRow
                key={user.session}
                user={
                  user.session === session.sessionId
                    ? { ...user, selfMute: micOff, selfDeaf: snapshot.deafened }
                    : user
                }
                self={user.session === session.sessionId}
                speaking={snapshot.speakers.includes(`voice.${user.session}`)}
                locallyMuted={localAudio[`voice.${user.session}`]?.muted ?? false}
                onOpen={openUser}
              />
            ))}
          </aside>
        </div>
      )}
      <CapturePickerHost />
      {shareRequest && (
        <ShareStartDialog
          quality={preferences.screenQuality}
          onStart={startShare}
          onClose={() => setShareRequest(null)}
        />
      )}
      {settings && (
        <SettingsDialog
          preferences={preferences}
          media={media}
          capabilities={capabilities}
          appInfo={appInfo}
          onChange={changePreferences}
          onAdjust={changeRanges}
          onClose={() => setSettings(false)}
        />
      )}
      {selectedUser && (
        <ParticipantControls
          user={selectedUser}
          preference={localAudio[`voice.${selectedUser.session}`] ?? defaultLocalAudio}
          onVolume={(gain) => changeLocalAudio(`voice.${selectedUser.session}`, { gain })}
          onMuted={(muted) => changeLocalAudio(`voice.${selectedUser.session}`, { muted })}
          onClose={() => setSelectedUser(null)}
        />
      )}
    </div>
  );
}
