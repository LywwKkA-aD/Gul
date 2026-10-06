import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// Only the Wails bridge is substituted; React, LiveKit and the media transport are real.
export default defineConfig({
  server: { host: '127.0.0.1', port: 9251, strictPort: true },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: [{
      find: '../../bindings/github.com/LywwKkA-aD/Gul/services',
      replacement: fileURLToPath(new URL('./e2e/panel/services.ts', import.meta.url)),
    }],
  },
});
