import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Сборка кладётся в internal/web/dist и встраивается в бинарник панели.
// npm run dev проксирует /api на панель, запущенную на 127.0.0.1:8080.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
});
