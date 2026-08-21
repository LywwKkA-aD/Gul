import { Events } from '@wailsio/runtime';
import { useGulStore } from './store';
import type { ChannelNode, ChatMessage, ConnectionStatus, TofuPrompt } from './types';

// Event names mirror internal/domain/events.go.
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

  const store = useGulStore.getState();

  Events.On(CONNECTION_STATE, (e: { data: ConnectionStatus }) => {
    useGulStore.getState().setStatus(e.data);
  });
  Events.On(CHANNELS_TREE, (e: { data: ChannelNode }) => {
    useGulStore.getState().setTree(e.data);
  });
  Events.On(CHAT_MESSAGE, (e: { data: ChatMessage }) => {
    useGulStore.getState().appendMessage(e.data);
  });
  Events.On(TOFU_MISMATCH, (e: { data: TofuPrompt }) => {
    useGulStore.getState().setTofu(e.data);
  });

  void store; // initial state comes from the first pushed events
}
