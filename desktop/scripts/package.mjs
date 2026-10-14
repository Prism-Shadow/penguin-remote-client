import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { buildNative } from "./build-native.mjs";

const desktopDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const platform = process.argv[2];
const arch = process.argv[3] ?? "x64";
const builderPlatform = { win: "--win", mac: "--mac", linux: "--linux" }[platform];
const builderArch = { x64: "--x64", arm64: "--arm64" }[arch];

if (!builderPlatform || !builderArch) {
  console.error("Usage: node scripts/package.mjs <win|mac|linux> <x64|arm64>");
  process.exit(1);
}

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: desktopDir,
    env: process.env,
    encoding: "utf8",
    stdio: "inherit",
  });
  if (result.error) console.error(result.error);
  if (result.status !== 0) process.exit(result.status ?? 1);
}

run(process.execPath, ["scripts/check-legal.mjs"]);
buildNative(platform, arch);
const pnpmEntry = process.env.npm_execpath;
const runPnpm = (args) => {
  if (pnpmEntry) run(process.execPath, [pnpmEntry, ...args]);
  else run("pnpm", args);
};
const electronExecutable = path.join(
  desktopDir,
  "node_modules",
  "electron",
  "dist",
  process.platform === "win32" ? "electron.exe" : "electron",
);
if (!fs.existsSync(electronExecutable)) runPnpm(["exec", "install-electron"]);
runPnpm(["build"]);
runPnpm(["exec", "electron-builder", builderPlatform, builderArch, "--publish", "never"]);
run(process.execPath, ["scripts/verify-package.mjs", platform, arch]);
