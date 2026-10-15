import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { buildNative } from "./build-native.mjs";
import evsign from "../build/evsign.cjs";

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
if (platform === "win") {
  // Sign the source once; electron-builder excludes this executable from its hook.
  await evsign({ path: path.join(desktopDir, "native/penguin-remote.exe"), hash: "sha256" });
}
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
const signingArgs = [];
if (platform === "mac") {
  if (process.env.REQUIRE_MACOS_SIGNING === "true") signingArgs.push("-c.mac.forceCodeSigning=true");
  else if (!process.env.CSC_LINK) {
    process.env.CSC_IDENTITY_AUTO_DISCOVERY = "false";
    signingArgs.push("-c.mac.hardenedRuntime=false");
  }
}
runPnpm(["exec", "electron-builder", builderPlatform, builderArch, "--publish", "never", ...signingArgs]);
run(process.execPath, ["scripts/verify-package.mjs", platform, arch]);
