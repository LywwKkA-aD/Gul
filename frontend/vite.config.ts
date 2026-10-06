import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath } from "node:url";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), tailwindcss(), wails("./bindings")],
  build: {
    rolldownOptions: { input: {
      main: fileURLToPath(new URL('./index.html', import.meta.url)),
      screen: fileURLToPath(new URL('./screen.html', import.meta.url)),
    } },
  },
});
