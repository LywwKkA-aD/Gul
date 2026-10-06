import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../bin/livekit-local/remote-fixture', emptyOutDir: true,
    rolldownOptions: { input: fileURLToPath(new URL('./e2e/remote/index.html', import.meta.url)) },
  },
  preview: { host: '127.0.0.1', port: 9253, strictPort: true },
});
