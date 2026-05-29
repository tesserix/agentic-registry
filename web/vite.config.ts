import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The dev server proxies /v0, /v0.1 and /mcp to the local registry backend so
// the SPA talks to the real API without CORS during development.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/v0": "http://localhost:8080",
      "/v0.1": "http://localhost:8080",
      "/mcp": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
});
