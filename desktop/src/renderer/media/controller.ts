import {
  LogLevel,
  type Room,
  RoomEvent,
  Track,
  setLogLevel,
  type LocalVideoTrack,
  type RemoteAudioTrack,
  type RemoteParticipant,
  type RemoteTrackPublication,
  type RoomEventCallbacks,
} from 'livekit-client';
import type { AudioState, MediaGrant, MediaSession } from '../../shared/contracts.ts';
import { audioElement, captureScreen } from './capture.ts';
import {
  initialSnapshot,
  type Dependencies,
  type ScreenCapture,
  type ScreenInfo,
  type Snapshot,
} from './model.ts';
import { chatText, latency, participantId, validGrant, validText } from './protocol.ts';
import { createRoom, disconnect, unpublish } from './rooms.ts';
import { Playback } from './playback.ts';
import { Devices } from './devices.ts';
import { Microphone } from './microphone.ts';
import type { VoiceSettings } from './voice-gate.ts';
import { screenPublishOptions } from './screen-settings.ts';
export type { Snapshot, ScreenCapture, ScreenInfo } from './model.ts';

interface Capture extends ScreenCapture {
  readonly room: Room;
  readonly generation: number;
  readonly ended: () => void;
  released: boolean;
}

/** Chromium owns microphone, screen, A/V playback and WebRTC. Main owns credentials and REALITY. */
export class MediaController {
  private readonly dependencies: Dependencies;
  private readonly roomFactory: (kind: 'voice' | 'screen') => Room;
  private readonly listeners = new Set<() => void>();
  private snapshot = initialSnapshot();
  private session?: MediaSession;
  private voice?: Room;
  private screen?: Room;
  private openingScreen?: Promise<Room | undefined>;
  private readonly mic: Microphone;
  private capture?: Capture;
  private stagedCapture?: ScreenCapture;
  private readonly discarded = new WeakSet<ScreenCapture>();
  private epoch = 0;
  private captureGeneration = 0;
  private watchGeneration = 0;
  private watching: string | null = null;
  private readonly screens = new Map<string, ScreenInfo>();
  private readonly playback: Playback;
  private readonly unbind = new Map<Room, () => void>();
  private audioQueue: Promise<void> = Promise.resolve();
  private preferences: AudioState = { muted: false, deafened: false };
  private audioRevision = 0;
  private joinRevision = 0;
  private readonly devices = new Devices();
  private timer?: ReturnType<typeof setInterval>;
  private chatSequence = 0;

  constructor(dependencies: Dependencies) {
    this.dependencies = dependencies;
    this.roomFactory = dependencies.roomFactory ?? createRoom;
    this.playback = new Playback(dependencies.audioElementFactory ?? audioElement, () => {
      this.update({ warning: 'Нажмите в окне приложения, чтобы включить воспроизведение звука.' });
    });
    this.mic = new Microphone({
      capture: dependencies.micFactory,
      processor: dependencies.voiceProcessorFactory,
      warning: (warning, muted) => this.update({ warning, ...(muted ? { muted: true } : {}) }),
      reading: ({ level, active, available }) => {
        const micLevel = Math.round(level * 1000) / 1000;
        if (
          this.snapshot.micLevel !== micLevel ||
          this.snapshot.voiceActive !== active ||
          this.snapshot.voiceProcessingAvailable !== available
        )
          this.update({ micLevel, voiceActive: active, voiceProcessingAvailable: available });
      },
    });
    setLogLevel(LogLevel.silent);
  }
  getSnapshot = (): Snapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private update(patch: Partial<Snapshot>) {
    this.snapshot = Object.freeze({
      ...this.snapshot,
      ...patch,
      participants: patch.participants ? Object.freeze([...patch.participants]) : this.snapshot.participants,
      screens: patch.screens ? Object.freeze([...patch.screens]) : this.snapshot.screens,
      videos: patch.videos ? Object.freeze([...patch.videos]) : this.snapshot.videos,
      chat: patch.chat ? Object.freeze([...patch.chat]) : this.snapshot.chat,
      speakers: patch.speakers ? Object.freeze([...patch.speakers]) : this.snapshot.speakers,
    });
    this.listeners.forEach((listener) => listener());
  }
  join = async (session: MediaSession): Promise<void> => {
    const revision = ++this.joinRevision;
    await this.teardown();
    if (this.joinRevision !== revision) return;
    const epoch = ++this.epoch;
    if (!validGrant(session.grant, session, 'voice')) {
      this.update({ error: 'Недействительные параметры голосового канала.' });
      return;
    }
    this.session = session;
    const room = this.roomFactory('voice');
    this.voice = room;
    this.update({ state: 'connecting', error: '', warning: '', ...this.preferences });
    this.bind(room, 'voice', epoch);
    try {
      await room.connect(session.grant.url, session.grant.token, {
        autoSubscribe: false,
        rtcConfig: { iceTransportPolicy: 'relay' },
      });
      if (this.epoch !== epoch || this.voice !== room) {
        await disconnect(room);
        return;
      }
      await this.applyOutput(room, epoch);
      if (this.epoch !== epoch || this.voice !== room) {
        await disconnect(room);
        return;
      }
      this.update({ state: 'connected' });
      this.participants(room);
      room.remoteParticipants.forEach((participant) =>
        participant.trackPublications.forEach((pub) => this.published(room, pub, participant, 'voice')),
      );
      this.timer = setInterval(() => {
        void this.measureLatency(epoch);
      }, 1000);
      this.timer.unref?.();
      await this.enableMicrophone(room, epoch);
    } catch {
      if (this.epoch === epoch) {
        await this.leave();
        if (this.epoch === epoch + 1)
          this.update({ error: 'Не удалось подключить голосовой канал. Повторите подключение.' });
      } else await disconnect(room);
    }
  };
  private async enableMicrophone(room: Room, epoch: number) {
    this.mic.apply({ muted: this.snapshot.muted, deafened: this.snapshot.deafened });
    await this.mic.start(
      room,
      this.devices.microphoneDevice,
      () => this.epoch === epoch && this.voice === room,
    );
  }
  leave = (): Promise<void> => {
    ++this.joinRevision;
    return this.teardown();
  };
  private async teardown(): Promise<void> {
    ++this.epoch;
    ++this.watchGeneration;
    ++this.audioRevision;
    this.audioQueue = Promise.resolve();
    if (this.timer) clearInterval(this.timer);
    this.timer = undefined;
    const voice = this.voice;
    this.voice = undefined;
    this.session = undefined;
    const closingMicrophone = this.mic.stop();
    this.unbind.get(voice!)?.();
    this.unbind.delete(voice!);
    const closingScreen = this.closeScreen();
    this.playback.reset();
    this.screens.clear();
    this.update({ ...initialSnapshot(), ...this.preferences, voiceSettings: this.mic.settings });
    await Promise.all([closingScreen, closingMicrophone, disconnect(voice)]);
  }
  setAudio = (state: AudioState): Promise<void> => {
    const epoch = this.epoch;
    const revision = ++this.audioRevision;
    this.preferences = Object.freeze({ muted: Boolean(state.muted), deafened: Boolean(state.deafened) });
    // PTT affects Chromium immediately; metadata reconciliation must not delay capture.
    this.update({
      ...this.preferences,
      ...(state.muted || state.deafened ? { micLevel: 0, voiceActive: false } : {}),
    });
    this.applyAudio();
    if (this.mic.captured)
      void this.mic
        .synchronize(state)
        .then(() => {
          if (this.epoch === epoch) this.applyAudio();
        })
        .catch(() => {});
    const operation = this.audioQueue
      .catch(() => {})
      .then(async () => {
        if (!this.session || this.epoch !== epoch) return;
        try {
          const confirmed = await this.dependencies.audioState(state);
          if (this.epoch !== epoch || this.audioRevision !== revision) return;
          this.preferences = Object.freeze({ muted: confirmed.muted, deafened: confirmed.deafened });
          this.update({ ...confirmed, error: '' });
          this.applyAudio();
          if (!this.mic.captured && !confirmed.muted && !confirmed.deafened && this.voice)
            await this.enableMicrophone(this.voice, epoch);
          else if (this.mic.captured) await this.mic.synchronize(confirmed);
        } catch {
          if (this.epoch === epoch && this.audioRevision === revision) {
            // Broker metadata failure must never undo the user's local mute/deafen.
            this.update({
              error: 'Настройки звука применены локально. Не удалось обновить состояние на сервере.',
            });
            this.applyAudio();
          }
        }
      });
    this.audioQueue = operation;
    return operation;
  };
  setDevice = async (kind: 'audioinput' | 'audiooutput', id: string): Promise<void> => {
    const epoch = this.epoch;
    const voice = this.voice;
    try {
      await this.devices.set(
        kind,
        id,
        voice,
        () => this.screen,
        () => this.epoch === epoch && this.voice === voice,
      );
      this.mic.useDevice(this.devices.microphoneDevice);
      if (this.epoch === epoch) this.applyAudio();
    } catch {
      if (this.epoch === epoch) this.update({ error: 'Не удалось выбрать аудиоустройство.' });
      throw new Error('Не удалось выбрать аудиоустройство.');
    }
  };
  setVoiceSettings = async (patch: Partial<VoiceSettings>): Promise<void> => {
    await this.mic.configure(patch);
    this.update({ voiceSettings: this.mic.settings });
  };
  private async applyOutput(room: Room, epoch: number) {
    try {
      await this.devices.applyOutput(room);
    } catch {
      if (this.epoch === epoch)
        this.update({
          warning: 'Сохранённое устройство вывода недоступно. Используется устройство системы.',
        });
    }
  }
  setUserVolume = (identity: string, gain: number): void => {
    this.playback.setUserVolume(identity, gain);
  };
  setUserMuted = (identity: string, muted: boolean): void => {
    this.playback.setUserMuted(identity, muted);
  };
  sendChat = async (text: string): Promise<void> => {
    text = text.trim();
    if (!validText(text) || !this.voice || !this.session || this.snapshot.state !== 'connected')
      throw new Error('Сообщение недоступно или слишком длинное.');
    const epoch = this.epoch;
    const session = this.session;
    try {
      await this.voice.localParticipant.publishData(new TextEncoder().encode(JSON.stringify({ text })), {
        reliable: true,
        topic: 'gul.chat.v1',
      });
      if (this.epoch === epoch) this.appendChat(session.identity, session.name, text, true);
    } catch {
      throw new Error('Не удалось отправить сообщение.');
    }
  };
  startScreen = async (input: MediaGrant | Promise<MediaGrant>, withAudio: boolean): Promise<void> => {
    // Handle grant rejection immediately while an OS picker may remain open.
    const granted = Promise.resolve(input).then(
      (value) => value,
      () => undefined,
    );
    const session = this.session;
    if (!session || this.snapshot.state !== 'connected' || this.snapshot.pendingShare || this.capture) return;
    if ('url' in input && !validGrant(input, session, 'screen')) {
      this.update({ error: 'Недействительные параметры демонстрации.' });
      return;
    }
    const epoch = this.epoch;
    const generation = ++this.captureGeneration;
    this.update({ pendingShare: true, error: '', warning: '' });
    let captured: ScreenCapture | undefined;
    let capture: Capture | undefined;
    try {
      // Trigger selection before the first await so the original gesture reaches Chromium.
      captured = await (this.dependencies.captureFactory ?? captureScreen)(withAudio);
      if (this.epoch !== epoch || generation !== this.captureGeneration) {
        this.discard(captured);
        return;
      }
      this.stagedCapture = captured;
      const grant = await granted;
      if (!grant || !validGrant(grant, session, 'screen')) throw new Error('Invalid screen grant');
      if (this.epoch !== epoch || generation !== this.captureGeneration) {
        this.discard(captured);
        return;
      }
      const room = await this.ensureScreen(grant, epoch);
      if (!room || this.epoch !== epoch || generation !== this.captureGeneration) {
        this.discard(captured);
        return;
      }
      const video = captured.tracks.filter((track) => track.kind === Track.Kind.Video);
      const audio = captured.tracks.filter((track) => track.kind === Track.Kind.Audio);
      if (
        video.length !== 1 ||
        audio.length > 1 ||
        (!withAudio && audio.length) ||
        video.length + audio.length !== captured.tracks.length
      )
        throw new Error('Invalid capture');
      capture = {
        ...captured,
        room,
        generation,
        released: false,
        ended: () => {
          if (this.capture === capture) void this.stopScreen();
        },
      };
      this.stagedCapture = undefined;
      this.capture = capture;
      captured.tracks.forEach((track) => track.on('ended', capture!.ended));
      if (captured.tracks.some((track) => track.mediaStreamTrack.readyState === 'ended'))
        throw new Error('Capture ended');
      await video[0].mediaStreamTrack.applyConstraints({
        width: { max: 1280 },
        height: { max: 720 },
        frameRate: { max: 30 },
      });
      const h264 = (await this.dependencies.preferH264?.()) ?? false;
      for (const track of captured.tracks) {
        if (!this.currentCapture(capture)) return;
        const isVideo = track.kind === Track.Kind.Video;
        const publication = await room.localParticipant.publishTrack(
          track,
          screenPublishOptions(isVideo, h264),
        );
        if (!this.currentCapture(capture)) {
          await unpublish(room, track);
          return;
        }
        if (isVideo)
          this.update({
            videos: [
              ...this.snapshot.videos,
              Object.freeze({ id: publication.trackSid, identity: grant.identity, local: true, track }),
            ],
          });
      }
      if (this.currentCapture(capture))
        this.update({
          sharing: true,
          pendingShare: false,
          screenAudio: audio.length ? 'capturing' : withAudio ? 'unavailable' : 'off',
          warning:
            withAudio && !audio.length ? 'Источник не передал звук. Выберите доступный источник аудио.' : '',
        });
    } catch {
      if (capture) await this.release(capture);
      else if (captured) {
        this.discard(captured);
        if (this.stagedCapture === captured) this.stagedCapture = undefined;
      }
      if (this.epoch === epoch && generation === this.captureGeneration) {
        this.capture = undefined;
        this.update({
          pendingShare: false,
          sharing: false,
          screenAudio: 'off',
          videos: this.snapshot.videos.filter((video) => !video.local),
          error: 'Не удалось начать демонстрацию. Проверьте разрешение на захват экрана.',
        });
        if (!this.watching) await this.closeScreen();
      }
    }
  };
  stopScreen = async (): Promise<void> => {
    ++this.captureGeneration;
    const capture = this.capture;
    this.capture = undefined;
    if (this.stagedCapture) this.discard(this.stagedCapture);
    this.stagedCapture = undefined;
    this.update({
      sharing: false,
      pendingShare: false,
      screenAudio: 'off',
      videos: this.snapshot.videos.filter((video) => !video.local),
    });
    if (capture) await this.release(capture);
    if (!this.watching) await this.closeScreen();
  };
  watchScreen = async (identity: string | null): Promise<void> => {
    if (
      identity &&
      (!participantId(identity, 'screen') || participantId(identity, 'screen') === this.session?.sessionId)
    )
      return;
    const epoch = this.epoch;
    const generation = ++this.watchGeneration;
    this.watching = identity;
    this.refreshScreens();
    this.clearScreenPlayback();
    this.screen?.remoteParticipants.forEach((p) =>
      p.trackPublications.forEach((pub) => pub.setSubscribed(false)),
    );
    if (!identity) {
      if (!this.capture) await this.closeScreen();
      else
        this.screen?.remoteParticipants.forEach((p) =>
          p.trackPublications.forEach((pub) => pub.setSubscribed(false)),
        );
      return;
    }
    if (!this.session || this.snapshot.state !== 'connected') return;
    try {
      const grant = await this.dependencies.screenGrant();
      if (this.epoch !== epoch || generation !== this.watchGeneration) return;
      const room = await this.ensureScreen(grant, epoch);
      if (room && this.epoch === epoch && generation === this.watchGeneration)
        room.remoteParticipants.forEach((p) =>
          p.trackPublications.forEach((pub) => this.subscribeScreen(pub, p)),
        );
    } catch {
      if (this.epoch === epoch && generation === this.watchGeneration) {
        this.watching = null;
        this.refreshScreens();
        this.update({ error: 'Не удалось открыть демонстрацию. Повторите попытку.' });
        if (!this.capture) await this.closeScreen();
      }
    }
  };
  private async ensureScreen(grant: MediaGrant, epoch: number): Promise<Room | undefined> {
    if (!this.session || !validGrant(grant, this.session, 'screen') || this.epoch !== epoch)
      throw new Error('Invalid screen grant');
    if (this.openingScreen) return this.openingScreen;
    if (this.screen) return this.screen;
    const room = this.roomFactory('screen');
    this.screen = room;
    this.bind(room, 'screen', epoch);
    const opening = (async () => {
      try {
        await room.connect(grant.url, grant.token, {
          autoSubscribe: false,
          rtcConfig: { iceTransportPolicy: 'relay' },
        });
        if (this.epoch !== epoch || this.screen !== room) {
          await disconnect(room);
          return;
        }
        await this.applyOutput(room, epoch);
        room.remoteParticipants.forEach((p) =>
          p.trackPublications.forEach((pub) => this.subscribeScreen(pub, p)),
        );
        return room;
      } catch {
        if (this.screen === room) await this.closeScreen();
        else await disconnect(room);
        throw new Error('Screen unavailable');
      }
    })();
    this.openingScreen = opening;
    try {
      return await opening;
    } finally {
      if (this.openingScreen === opening) this.openingScreen = undefined;
    }
  }
  private async closeScreen() {
    const room = this.screen;
    this.screen = undefined;
    this.openingScreen = undefined;
    this.watching = null;
    this.unbind.get(room!)?.();
    this.unbind.delete(room!);
    ++this.captureGeneration;
    const capture = this.capture;
    this.capture = undefined;
    if (this.stagedCapture) this.discard(this.stagedCapture);
    this.stagedCapture = undefined;
    this.clearScreenPlayback();
    this.update({ sharing: false, pendingShare: false, screenAudio: 'off', videos: [] });
    this.refreshScreens();
    await Promise.all([capture ? this.release(capture) : undefined, disconnect(room)]);
  }
  private currentCapture(capture: Capture) {
    return (
      this.capture === capture &&
      !capture.released &&
      this.captureGeneration === capture.generation &&
      this.screen === capture.room
    );
  }
  private discard(capture: ScreenCapture) {
    if (this.discarded.has(capture)) return;
    this.discarded.add(capture);
    capture.tracks.forEach((track) => track.stop());
    capture.cleanup?.();
  }
  private async release(capture: Capture) {
    if (capture.released) return;
    capture.released = true;
    capture.tracks.forEach((track) => {
      track.off('ended', capture.ended);
      track.stop();
    });
    capture.cleanup?.();
    await Promise.all(capture.tracks.map((track) => unpublish(capture.room, track)));
  }
  private clearScreenPlayback() {
    this.playback.clear('screen');
    this.update({ videos: this.snapshot.videos.filter((video) => video.local) });
  }
  private refreshScreens() {
    this.update({
      screens: [...this.screens.values()].map((screen) =>
        Object.freeze({
          ...screen,
          watching: screen.identity === this.watching,
          state:
            screen.state === 'paused'
              ? 'paused'
              : screen.identity === this.watching
                ? 'watching'
                : 'available',
        }),
      ),
    });
  }
  private participants(room: Room) {
    if (!this.session) return;
    this.update({
      participants: [
        Object.freeze({ identity: this.session.identity, name: this.session.name }),
        ...[...room.remoteParticipants.values()]
          .filter((p) => participantId(p.identity, 'voice'))
          .map((p) => Object.freeze({ identity: p.identity, name: p.name || p.identity })),
      ],
    });
  }
  private published(
    room: Room,
    pub: RemoteTrackPublication,
    participant: RemoteParticipant,
    kind: 'voice' | 'screen',
  ) {
    if (kind === 'screen') {
      this.subscribeScreen(pub, participant);
      return;
    }
    if (
      participantId(participant.identity, 'voice') &&
      participant.identity !== this.session?.identity &&
      pub.source === Track.Source.Microphone
    )
      pub.setSubscribed(true);
    if (!participantId(participant.identity, 'screen')) return;
    if (pub.source === Track.Source.ScreenShareAudio && this.screens.has(participant.identity)) {
      this.screens.set(participant.identity, {
        ...this.screens.get(participant.identity)!,
        audioSid: pub.trackSid,
      });
      this.refreshScreens();
      return;
    }
    if (pub.source !== Track.Source.ScreenShare) return;
    const id = participantId(participant.identity, 'screen')!;
    const audio = [...participant.trackPublications.values()].find(
      (track) => track.source === Track.Source.ScreenShareAudio,
    );
    this.screens.set(
      participant.identity,
      Object.freeze({
        identity: participant.identity,
        ownerIdentity: `voice.${id}`,
        name: participant.name || participant.identity,
        videoSid: pub.trackSid,
        audioSid: audio?.trackSid,
        local: id === this.session?.sessionId,
        watching: false,
        state: pub.isMuted ? 'paused' : 'available',
      }),
    );
    this.refreshScreens();
  }
  private subscribeScreen(pub: RemoteTrackPublication, participant: RemoteParticipant) {
    if (pub.source === Track.Source.ScreenShare || pub.source === Track.Source.ScreenShareAudio)
      pub.setSubscribed(
        participant.identity === this.watching && Boolean(participantId(participant.identity, 'screen')),
      );
  }
  private subscribed(
    room: Room,
    track: Track,
    pub: RemoteTrackPublication,
    participant: RemoteParticipant,
    kind: 'voice' | 'screen',
  ) {
    const own =
      participantId(participant.identity, kind === 'voice' ? 'voice' : 'screen') === this.session?.sessionId;
    if (own) return;
    const accepted =
      kind === 'voice'
        ? participantId(participant.identity, 'voice') && pub.source === Track.Source.Microphone
        : participant.identity === this.watching &&
          participantId(participant.identity, 'screen') &&
          (pub.source === Track.Source.ScreenShare || pub.source === Track.Source.ScreenShareAudio);
    if (!accepted) return;
    if (track.kind === Track.Kind.Video && kind === 'screen')
      this.update({
        videos: [
          ...this.snapshot.videos.filter((v) => v.id !== pub.trackSid),
          Object.freeze({ id: pub.trackSid, identity: participant.identity, local: false, track }),
        ],
      });
    if (track.kind === Track.Kind.Audio) {
      const audio = track as RemoteAudioTrack;
      this.playback.attach(
        pub.trackSid,
        participant.identity,
        audio,
        kind,
        Boolean(room.options.webAudioMix),
      );
      this.applyAudio();
    }
  }
  private applyAudio() {
    this.mic.apply({ muted: this.snapshot.muted, deafened: this.snapshot.deafened });
    this.playback.apply(this.snapshot.deafened);
  }
  private appendChat(identity: string, name: string, text: string, local: boolean) {
    const entry = Object.freeze({
      id: ++this.chatSequence,
      identity,
      name,
      text,
      local,
      time: (this.dependencies.now ?? Date.now)(),
    });
    this.update({ chat: [...this.snapshot.chat.slice(-499), entry] });
  }
  private bind(room: Room, kind: 'voice' | 'screen', epoch: number) {
    const cleanups: (() => void)[] = [];
    const current = () =>
      this.epoch === epoch && (kind === 'voice' ? this.voice === room : this.screen === room);
    const on = <K extends keyof RoomEventCallbacks>(event: K, handler: RoomEventCallbacks[K]) => {
      room.on(event, handler);
      cleanups.push(() => {
        room.off(event, handler);
      });
    };
    this.unbind.set(room, () => {
      cleanups.forEach((cleanup) => cleanup());
    });
    on(RoomEvent.ParticipantConnected, (p) => {
      if (current()) {
        if (kind === 'voice') this.participants(room);
        p.trackPublications.forEach((pub) => this.published(room, pub, p, kind));
      }
    });
    on(RoomEvent.ParticipantDisconnected, (p) => {
      if (current()) {
        this.screens.delete(p.identity);
        this.refreshScreens();
        this.playback.removeParticipant(p.identity);
        this.update({ videos: this.snapshot.videos.filter((v) => v.identity !== p.identity) });
        if (kind === 'voice') this.participants(room);
      }
    });
    on(RoomEvent.TrackPublished, (pub, p) => {
      if (current()) this.published(room, pub, p, kind);
    });
    on(RoomEvent.TrackSubscribed, (track, pub, p) => {
      if (current()) this.subscribed(room, track, pub, p, kind);
    });
    on(RoomEvent.TrackUnsubscribed, (_track, pub) => {
      if (current()) {
        this.playback.remove(pub.trackSid);
        this.update({ videos: this.snapshot.videos.filter((v) => v.id !== pub.trackSid) });
      }
    });
    on(RoomEvent.TrackUnpublished, (pub, p) => {
      if (!current()) return;
      if (pub.source === Track.Source.ScreenShare) {
        this.screens.delete(p.identity);
        this.refreshScreens();
        if (this.watching === p.identity) void this.watchScreen(null);
      } else if (pub.source === Track.Source.ScreenShareAudio && this.screens.has(p.identity)) {
        this.screens.set(p.identity, { ...this.screens.get(p.identity)!, audioSid: undefined });
        this.refreshScreens();
      }
    });
    on(RoomEvent.TrackMuted, (pub, p) => {
      if (current() && this.screens.has(p.identity) && pub.source === Track.Source.ScreenShare) {
        this.screens.set(p.identity, { ...this.screens.get(p.identity)!, state: 'paused' });
        this.refreshScreens();
      }
    });
    on(RoomEvent.TrackUnmuted, (pub, p) => {
      if (current() && pub.source === Track.Source.ScreenShare)
        this.published(room, pub as RemoteTrackPublication, p as RemoteParticipant, kind);
    });
    on(RoomEvent.ActiveSpeakersChanged, (participants) => {
      if (current() && kind === 'voice')
        this.update({
          speakers: participants.filter((p) => participantId(p.identity, 'voice')).map((p) => p.identity),
        });
    });
    on(RoomEvent.DataReceived, (data, p, _packetKind, topic) => {
      if (current() && kind === 'voice' && p && p.identity !== this.session?.identity) {
        const text = chatText(data, p.identity, topic);
        if (text !== undefined) this.appendChat(p.identity, p.name || p.identity, text, false);
      }
    });
    const reconnecting = () => {
      if (!current()) return;
      void this.closeScreen();
      if (kind === 'voice') this.update({ state: 'reconnecting', pingMs: null });
    };
    on(RoomEvent.Reconnecting, reconnecting);
    on(RoomEvent.SignalReconnecting, reconnecting);
    on(RoomEvent.Reconnected, () => {
      if (current() && kind === 'voice') this.update({ state: 'connected' });
    });
    on(RoomEvent.Disconnected, () => {
      if (!current() || this.snapshot.state === 'connecting') return;
      if (kind === 'screen') {
        void this.closeScreen();
        this.update({ warning: 'Демонстрация отключилась. Откройте её повторно.' });
      } else {
        void this.leave().then(() => {
          if (this.epoch === epoch + 1) this.update({ error: 'Соединение с голосовым каналом потеряно.' });
        });
      }
    });
  }
  private async measureLatency(epoch: number) {
    if (this.epoch !== epoch || this.snapshot.state !== 'connected') return;
    const sources = [this.mic?.sender, ...this.playback.receivers('voice')];
    const samples = await Promise.all(
      sources.map(async (source) => {
        try {
          return source ? latency(await source.getStats()) : undefined;
        } catch {
          return;
        }
      }),
    );
    if (this.epoch === epoch) {
      const valid = samples.filter((value): value is number => value !== undefined);
      this.update({ pingMs: valid.length ? Math.round(Math.max(...valid)) : null });
    }
  }
}
