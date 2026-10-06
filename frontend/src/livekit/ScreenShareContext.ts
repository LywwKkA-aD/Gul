import { createContext, useContext } from 'react';
import type { LiveKitController, LiveKitSnapshot } from './controller';

interface ScreenShareContextValue {
  readonly controller: LiveKitController;
  readonly snapshot: LiveKitSnapshot;
  readonly canCapture: boolean;
}

// A single authenticated channel owns both the bottom-bar control and its video
// tiles. Outside that scope the control is disabled and no capture can start.
export const ScreenShareContext = createContext<ScreenShareContextValue | null>(null);
export const useScreenShare = () => useContext(ScreenShareContext);
