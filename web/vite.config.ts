import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, run `pyro serve --no-token` and `npm run dev`; API calls are proxied.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { "/api": "http://127.0.0.1:7878" },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
