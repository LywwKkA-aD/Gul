import { fileURLToPath } from 'node:url';
import { mergeConfig } from 'vite';
import panelConfig from './vite.panel.config.ts';

// A separate single-entry production build preserves the actual MainScreen
// lazy import. Importing the Provider in the direct panel harness would preload it.
export default mergeConfig(panelConfig, {
  build: {
    outDir: '../bin/livekit-local/mainscreen-fixture',
    emptyOutDir: true,
    rolldownOptions: { input: fileURLToPath(new URL('./e2e/main/index.html', import.meta.url)) },
  },
  preview: { host: '127.0.0.1', port: 9252, strictPort: true },
});
