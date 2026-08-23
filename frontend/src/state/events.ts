import { Events } from '@wailsio/runtime';
import { SettingsService } from '../../bindings/github.com/LywwKkA-aD/Gul/services';
import { useGulStore } from './store';
import { normalizeSettings } from './settings';
import { normalizeTree } from './types';
import type {
  AudioLevels,
  ChatMessage,
  ConnectionLatency,
  ConnectionStatus,
  SelfAudioState,
  TalkingEvent,
  TofuPrompt,
  WireChannelNode,
} from './types';

// Event names mirror internal/domain/events.go. The wails runtime types these
// events from the generated bindings (eventdata.d.ts); payloads are cast to
// our local structural twins (enum values are the same string literals).
const CONNECTION_STATE = 'connection:state';
const CONNECTION_LATENCY = 'connection:latency';
const CHANNELS_TREE = 'channels:tree';
const CHAT_MESSAGE = 'chat:message';
const TOFU_MISMATCH = 'tofu:mismatch';
const USER_TALKING = 'user:talking';
const AUDIO_LEVELS = 'audio:levels';
const AUDIO_SELF = 'audio:self';
const AUDIO_PTT = 'audio:ptt';

let subscribed = false;

// subscribeGulEvents wires Wails push events into the zustand store.
// Call once at app start; repeated calls are no-ops.
export function subscribeGulEvents(): void {
  if (subscribed) return;
  subscribed = true;

  Events.On(CONNECTION_STATE, (e) => {
    const status = e.data as unknown as ConnectionStatus;
    useGulStore.getState().setStatus(status);
    // The Go side remembers the connect form once the server has accepted it
    // (internal/core/settings.go). Read it back, so a disconnect inside this
    // session finds the form the way the next start would fill it.
    if (status.state === 'connected') {
      SettingsService.Load()
        .then((settings) => {
          const remembered = normalizeSettings(settings);
          useGulStore.getState().setRememberedConnection(remembered.address, remembered.username);
        })
        .catch((err: unknown) => console.error('settings:', err));
    }
  });
  Events.On(CONNECTION_LATENCY, (e) => {
    const latency = e.data as unknown as ConnectionLatency;
    const pingMs = Number.isFinite(latency.pingMs) && latency.pingMs >= 0 ? latency.pingMs : null;
    useGulStore.getState().setPingMs(pingMs);
  });
  Events.On(CHANNELS_TREE, (e) => {
    useGulStore.getState().setTree(normalizeTree(e.data as unknown as WireChannelNode));
  });
  Events.On(CHAT_MESSAGE, (e) => {
    useGulStore.getState().appendMessage(e.data as unknown as ChatMessage);
  });
  Events.On(TOFU_MISMATCH, (e) => {
    useGulStore.getState().setTofu(e.data as unknown as TofuPrompt);
  });
  Events.On(USER_TALKING, (e) => {
    const talk = e.data as unknown as TalkingEvent;
    useGulStore.getState().setTalking(talk.session, talk.talking);
  });
  // Meters land here about every 50 ms while the engine runs. The store keeps
  // them as two numbers, so a selector reading one of them stays cheap.
  Events.On(AUDIO_LEVELS, (e) => {
    const levels = e.data as unknown as AudioLevels;
    useGulStore.getState().setLevels(levels.micDb, levels.outDb);
  });
  // Mute and deafen are reachable from the system tray as well, so the window
  // renders what core reports rather than what it last asked for. Core pushes
  // this from whichever path made the change and stays silent when a request
  // changes nothing (internal/core/selfaudio.go).
  Events.On(AUDIO_SELF, (e) => {
    const self = e.data as unknown as SelfAudioState;
    useGulStore.getState().setVoiceGates(self.muted === true, self.deafened === true);
  });

  // The global push-to-talk key fires with the window unfocused, so its held
  // state can only reach the mic indicator through this event; the window
  // listener still sets pttHeld directly. setPttHeld dedupes either way.
  Events.On(AUDIO_PTT, (e) => {
    const ptt = e.data as unknown as { held?: boolean };
    useGulStore.getState().setPttHeld(ptt.held === true);
  });
}
