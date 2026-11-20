import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// `npm run dev` proxies the API to a running `stiggy-dashboard`
// (STIGGY_DASHBOARD, default http://127.0.0.1:7358).
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { "/api": process.env.STIGGY_DASHBOARD ?? "http://127.0.0.1:7358" },
  },
  // Built into the binary's package, which embeds it (go:embed).
  build: { outDir: "../cmd/stiggy-dashboard/web", emptyOutDir: true, chunkSizeWarningLimit: 2500 },
  test: { environment: "node" },
});
