import { Events } from '@wailsio/runtime';
import { useGulStore } from './store';
import { normalizeTree } from './types';
import type { ChatMessage, ConnectionStatus, TofuPrompt, WireChannelNode } from './types';

// Event names mirror internal/domain/events.go. The wails runtime types these
// events from the generated bindings (eventdata.d.ts); payloads are cast to
// our local structural twins (enum values are the same string literals).
const CONNECTION_STATE = 'connection:state';
const CHANNELS_TREE = 'channels:tree';
const CHAT_MESSAGE = 'chat:message';
const TOFU_MISMATCH = 'tofu:mismatch';

let subscribed = false;

// subscribeGulEvents wires Wails push events into the zustand store.
// Call once at app start; repeated calls are no-ops.
export function subscribeGulEvents(): void {
  if (subscribed) return;
  subscribed = true;

  Events.On(CONNECTION_STATE, (e) => {
    useGulStore.getState().setStatus(e.data as unknown as ConnectionStatus);
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
}
