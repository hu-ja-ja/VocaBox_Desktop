import { defineConfig } from "vite";

export default defineConfig({
  server: {
    // Keep this in sync with devUrl in mygo.config.ts.
    port: 5173,
    strictPort: true,
    // MyGo's development files are not part of the frontend.
    watch: { ignored: ["**/.mygo/**", "**/build/**"] },
  },
});
