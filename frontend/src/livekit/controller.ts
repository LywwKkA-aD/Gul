import {
  LogLevel, Room, RoomEvent, Track, setLogLevel,
  type LocalAudioTrack, type LocalVideoTrack, type RemoteTrackPublication, type RoomEventCallbacks,
} from 'livekit-client';

export interface JoinGrant { url: string; token: string; identity: string; room: string }
export interface ScreenCapture {
  tracks: (LocalAudioTrack | LocalVideoTrack)[];
  cleanup?: () => void;
}
export interface ScreenTrack {
  readonly id: string;
  readonly participant: string;
  readonly displayName?: string;
  readonly kind: 'video' | 'audio';
  readonly local: boolean;
  readonly track: Track;
}
export interface LiveKitSnapshot {
  readonly status: 'disconnected' | 'connecting' | 'connected' | 'reconnecting';
  readonly identity: string;
  readonly participants: readonly string[];
  readonly sharing: boolean;
  readonly screenAudio: boolean;
  readonly pendingShare: boolean;
  readonly warning: string;
  readonly error: string;
  readonly tracks: readonly ScreenTrack[];
}
interface CaptureSession extends ScreenCapture {
  room: Room;
  generation: number;
  released: boolean;
  ended: () => void;
}
interface Dependencies {
  roomFactory?: () => Room;
  /** Native Gul receives screen audio through its own mixer and devices. */
  subscribeAudio?: boolean;
  /** The isolated lab disables external ICE; authenticated sessions use the server's configuration. */
  allowServerIce?: boolean;
  /** Opt-in transport verification; ICE servers still come from the authenticated SFU. */
  forceRelay?: boolean;
  stopSharingOnReconnect?: boolean;
}

const emptySnapshot = (): LiveKitSnapshot => Object.freeze({
  status: 'disconnected', identity: '', participants: Object.freeze([]),
  sharing: false, screenAudio: false, pendingShare: false,
  warning: '', error: '', tracks: Object.freeze([]),
});
const encoding = Object.freeze({ maxBitrate: 4_000_000, maxFramerate: 30 });
const noAudioWarning = 'Источник не передал звук. Для звука выберите вкладку или включите передачу системного звука.';

// This controller owns only LiveKit display media. The native voice audio path
// and the browser's microphone/camera are never touched.
export class LiveKitController {
  private readonly grantProvider: (identity: string) => Promise<JoinGrant>;
  private readonly roomFactory: () => Room;
  private readonly subscribeAudio: boolean;
  private readonly allowServerIce: boolean;
  private readonly forceRelay: boolean;
  private readonly stopSharingOnReconnect: boolean;
  private readonly listeners = new Set<() => void>();
  private snapshot = emptySnapshot();
  private room?: Room;
  private unbindRoom = () => {};
  private connectionGeneration = 0;
  private captureGeneration = 0;
  private capture?: CaptureSession;

  constructor(grantProvider: (identity: string) => Promise<JoinGrant>, dependencies: Dependencies = {}) {
    this.grantProvider = grantProvider;
    this.subscribeAudio = dependencies.subscribeAudio !== false;
    this.allowServerIce = dependencies.allowServerIce === true;
    this.forceRelay = dependencies.forceRelay === true;
    this.stopSharingOnReconnect = dependencies.stopSharingOnReconnect === true;
    this.roomFactory = dependencies.roomFactory ?? (() => new Room({
      adaptiveStream: true, dynacast: true, stopLocalTrackOnUnpublish: true,
    }));
    setLogLevel(LogLevel.error);
  }

  getSnapshot = (): LiveKitSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  private update(patch: Partial<LiveKitSnapshot>) {
    this.snapshot = Object.freeze({
      ...this.snapshot, ...patch,
      participants: Object.freeze([...(patch.participants ?? this.snapshot.participants)]),
      tracks: Object.freeze([...(patch.tracks ?? this.snapshot.tracks)]),
    });
    this.listeners.forEach((listener) => listener());
  }

  join = async (identity: string): Promise<void> => {
    const generation = ++this.connectionGeneration;
    const previous = this.room;
    this.room = undefined;
    this.unbindRoom();
    this.unbindRoom = () => {};
    const stopped = this.stopShare();
    this.update({ ...emptySnapshot(), identity: identity.trim(), status: 'connecting' });
    await Promise.all([stopped, disconnect(previous)]);
    if (generation !== this.connectionGeneration) return;
    let room: Room | undefined;
    try {
      const grant = await this.grantProvider(identity.trim());
      if (generation !== this.connectionGeneration) return;
      room = this.roomFactory();
      this.room = room;
      this.bind(room);
      const rtcConfig: RTCConfiguration = {};
      if (!this.allowServerIce || loopbackEndpoint(grant.url)) rtcConfig.iceServers = [];
      if (this.forceRelay) rtcConfig.iceTransportPolicy = 'relay';
      await room.connect(grant.url, grant.token, {
        autoSubscribe: false,
        ...(Object.keys(rtcConfig).length ? { rtcConfig } : {}),
      });
      if (generation !== this.connectionGeneration || this.room !== room) {
        await disconnect(room);
        return;
      }
      this.update({ status: 'connected', identity: grant.identity, error: '' });
      this.updateParticipants(room);
      room.remoteParticipants.forEach((participant) => {
        participant.trackPublications.forEach(this.subscribeScreen);
      });
    } catch {
      await disconnect(room);
      if (generation !== this.connectionGeneration) return;
      this.unbindRoom();
      this.unbindRoom = () => {};
      this.room = undefined;
      this.update({ ...emptySnapshot(), identity: identity.trim(), error: 'Не удалось подключить демонстрации к каналу.' });
    }
  };

  share = (): Promise<void> => this.shareWith(async () => {
    const room = this.room;
    if (!room) throw new Error('Room unavailable');
    const tracks = await room.localParticipant.createScreenTracks({
      audio: { restrictOwnAudio: true, echoCancellation: false, noiseSuppression: false, autoGainControl: false },
      systemAudio: 'include',
      resolution: { width: 1920, height: 1080, frameRate: 30 },
      selfBrowserSurface: 'exclude', contentHint: 'motion',
    });
    return { tracks: tracks as (LocalAudioTrack | LocalVideoTrack)[] };
  });

  shareWith = async (captureFactory: () => Promise<ScreenCapture>): Promise<void> => {
    const room = this.room;
    if (!room || this.snapshot.status !== 'connected' || this.snapshot.pendingShare || this.capture) return;
    const generation = ++this.captureGeneration;
    this.update({ pendingShare: true, warning: '', error: '' });
    let session: CaptureSession | undefined;
    try {
      // Invoke before the first await, preserving getDisplayMedia's user gesture.
      const captured = await captureFactory();
      session = {
        ...captured, room, generation, released: false,
        ended: () => { if (this.capture === session) void this.stopShare(); },
      };
      if (generation !== this.captureGeneration || this.room !== room) {
        await release(session);
        return;
      }
      this.capture = session;
      const video = captured.tracks.filter((track) => track.kind === Track.Kind.Video);
      const audio = captured.tracks.filter((track) => track.kind === Track.Kind.Audio);
      if (video.length !== 1 || audio.length > 1 || video.length + audio.length !== captured.tracks.length) {
        throw new Error('Invalid screen capture');
      }
      captured.tracks.forEach((track) => track.on('ended', session!.ended));
      if (captured.tracks.some((track) => track.mediaStreamTrack.readyState === 'ended')) {
        throw new Error('Screen capture ended');
      }
      // The SDK uses ideal resolution on Chromium; explicitly cap the source.
      await video[0].mediaStreamTrack.applyConstraints({
        width: { max: 1920 }, height: { max: 1080 }, frameRate: { max: 30 },
      });
      for (const track of captured.tracks) {
        if (!this.current(session)) return;
        const publication = await room.localParticipant.publishTrack(track, {
          source: track.kind === Track.Kind.Video ? Track.Source.ScreenShare : Track.Source.ScreenShareAudio,
          screenShareEncoding: encoding, videoEncoding: encoding, simulcast: false,
        });
        if (!this.current(session)) {
          // stopShare may have unpublished while publishTrack was still pending.
          await unpublish(room, track);
          return;
        }
        const entry = Object.freeze({
          id: publication.trackSid, participant: this.snapshot.identity,
          kind: track.kind as 'video' | 'audio', local: true, track,
        });
        this.update({ tracks: [...this.snapshot.tracks, entry] });
      }
      if (this.current(session)) {
        this.update({ sharing: true, screenAudio: audio.length > 0, pendingShare: false, warning: audio.length ? '' : noAudioWarning });
      }
    } catch {
      if (session) await release(session);
      if (generation !== this.captureGeneration || this.room !== room) return;
      this.capture = undefined;
      this.update({ sharing: false, screenAudio: false, pendingShare: false,
        tracks: this.snapshot.tracks.filter((track) => !track.local),
        error: 'Не удалось начать демонстрацию. Проверьте разрешение на захват экрана и повторите попытку.',
      });
    }
  };

  private current(session: CaptureSession): boolean {
    return this.capture === session && !session.released &&
      this.captureGeneration === session.generation && this.room === session.room;
  }

  stopShare = async (): Promise<void> => {
    ++this.captureGeneration;
    const session = this.capture;
    this.capture = undefined;
    this.update({ sharing: false, screenAudio: false, pendingShare: false, warning: '',
      tracks: this.snapshot.tracks.filter((track) => !track.local) });
    if (session) await release(session);
  };

  leave = async (): Promise<void> => {
    ++this.connectionGeneration;
    const room = this.room;
    this.room = undefined;
    this.unbindRoom();
    this.unbindRoom = () => {};
    const stopped = this.stopShare();
    this.update(emptySnapshot());
    await Promise.all([stopped, disconnect(room)]);
  };

  startAudio = async (): Promise<void> => {
    const room = this.room;
    if (!room) return;
    try {
      await room.startAudio();
    } catch {
      if (this.room === room) this.update({ warning: 'Браузер не разрешил воспроизведение. Нажмите «Включить звук» ещё раз.' });
    }
  };

  private updateParticipants(room: Room) {
    if (this.room !== room) return;
    this.update({ participants: [this.snapshot.identity, ...Array.from(room.remoteParticipants.values(), (participant) => participant.identity)].filter(Boolean).sort() });
  }

  private acceptsSource(source: Track.Source): boolean {
    return source === Track.Source.ScreenShare || (this.subscribeAudio && source === Track.Source.ScreenShareAudio);
  }

  private subscribeScreen = (publication: RemoteTrackPublication) => {
    if (this.acceptsSource(publication.source)) publication.setSubscribed(true);
  };

  private bind(room: Room) {
    const removers: (() => void)[] = [];
    const on = <K extends keyof RoomEventCallbacks>(event: K, handler: RoomEventCallbacks[K]) => {
      room.on(event, handler);
      removers.push(() => { room.off(event, handler); });
    };
    this.unbindRoom = () => { removers.forEach((remove) => remove()); };
    const reconnecting = () => {
      if (this.room !== room) return;
      if (this.stopSharingOnReconnect) void this.stopShare();
      this.update({ status: 'reconnecting' });
    };
    on(RoomEvent.Reconnecting, reconnecting);
    on(RoomEvent.SignalReconnecting, reconnecting);
    on(RoomEvent.Reconnected, () => { if (this.room === room) this.update({ status: 'connected' }); });
    on(RoomEvent.ParticipantConnected, (participant) => {
      this.updateParticipants(room);
      participant.trackPublications.forEach(this.subscribeScreen);
    });
    on(RoomEvent.TrackPublished, (publication) => {
      if (this.room === room) this.subscribeScreen(publication);
    });
    on(RoomEvent.ParticipantDisconnected, (participant) => {
      if (this.room !== room) return;
      this.update({ tracks: this.snapshot.tracks.filter((track) => track.participant !== participant.identity) });
      this.updateParticipants(room);
    });
    on(RoomEvent.TrackSubscribed, (track, publication, participant) => {
      if (this.room !== room || !this.acceptsSource(publication.source)) return;
      if (track.kind !== Track.Kind.Video && track.kind !== Track.Kind.Audio) return;
      if (!this.subscribeAudio && track.kind === Track.Kind.Audio) return;
      const entry = Object.freeze({ id: publication.trackSid, participant: participant.identity, displayName: participant.name || participant.identity, kind: track.kind, local: false, track });
      this.update({ tracks: [...this.snapshot.tracks.filter((item) => item.id !== entry.id), entry] });
    });
    on(RoomEvent.TrackUnsubscribed, (_track, publication) => {
      if (this.room === room) this.update({ tracks: this.snapshot.tracks.filter((item) => item.id !== publication.trackSid) });
    });
    on(RoomEvent.AudioPlaybackStatusChanged, (playing) => {
      if (this.subscribeAudio && this.room === room && !playing) this.update({ warning: 'Нажмите «Включить звук», чтобы браузер начал воспроизведение.' });
    });
    on(RoomEvent.Disconnected, () => {
      if (this.room !== room) return;
      void this.leave();
      this.update({ error: 'Соединение с демонстрациями потеряно.' });
    });
  }
}

function loopbackEndpoint(url: string): boolean {
  const hostname = new URL(url).hostname;
  return hostname === 'localhost' || hostname === '[::1]' || /^127(?:\.\d{1,3}){3}$/.test(hostname);
}

async function disconnect(room?: Room) {
  try { await room?.disconnect(true); } catch { /* SDK errors may contain credentials. */ }
}

async function unpublish(room: Room, track: LocalAudioTrack | LocalVideoTrack) {
  try { await room.localParticipant.unpublishTrack(track, false); } catch { /* Disconnect already removes publications. */ }
}

async function release(session: CaptureSession) {
  if (session.released) return;
  session.released = true;
  session.tracks.forEach((track) => {
    track.off('ended', session.ended);
    track.stop();
  });
  try { session.cleanup?.(); } catch { /* Release the remaining publications too. */ }
  await Promise.all(session.tracks.map((track) => unpublish(session.room, track)));
}
