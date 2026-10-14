import { defineConfig } from "tsup";

export default defineConfig({
  entry: ["src/main.ts"],
  format: ["esm"],
  platform: "node",
  target: "node20",
  bundle: true,
  clean: true,
  sourcemap: false,
  splitting: false,
  external: ["electron"],
});
