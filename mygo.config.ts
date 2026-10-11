import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "VocaBox",
  identifier: "io.github.hu-ja-ja.vocabox",
  version: "0.1.0",
  // `mygo dev` loads devUrl; `mygo build` embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
});
