import type { ScreenGrant } from '../../src/livekit/session';
import { useGulStore } from '../../src/state/store';

export const AudioService = {
  ToggleMute: async () => { useGulStore.getState().setVoiceGates(!useGulStore.getState().muted, false); },
  ToggleDeafen: async () => { useGulStore.getState().setVoiceGates(true, !useGulStore.getState().deafened); },
  SetUserVolume: async () => {},
  SetUserMute: async () => {},
};

export const ChatService = { History: async () => [], Send: async () => {} };
export const ChannelsService = { Join: async () => {} };
export const ConnectionService = { Disconnect: async () => {} };

export const ScreenShareService = {
  async Grant(epoch: number, channelId: number): Promise<ScreenGrant> {
    const response = await fetch('/test/screen-grant', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ epoch, channelId }),
    });
    if (!response.ok) throw new Error('Test grant unavailable');
    const grant: ScreenGrant = await response.json();
    window.gulPanelGrantReplies = (window.gulPanelGrantReplies ?? 0) + 1;
    return grant;
  },
};
