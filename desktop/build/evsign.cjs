"use strict";

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const signedBasenames = new Set([
  "Penguin Remote.exe",
  "penguin-remote.exe",
  "elevate.exe",
  "penguin-remote-win-x64.exe",
]);

async function evsign(configuration) {
  const file = path.resolve(configuration.path);
  const basename = path.basename(file);
  if ((configuration.hash || "").toLowerCase() !== "sha256") return;
  if (!signedBasenames.has(basename) && !basename.endsWith(".__uninstaller.exe")) return;

  const key = process.env.EVSIGN_KEY;
  if (!key) {
    if (process.env.EVSIGN_REQUIRE === "true") {
      throw new Error("EVSIGN_KEY is required for Windows release signing.");
    }
    console.log(`[evsign] skip ${basename} because EVSIGN_KEY is not configured.`);
    return;
  }
  const client = process.env.EVSIGN_CLIENT ||
    path.join(process.env.RUNNER_TEMP || os.tmpdir(), "evsign-client.exe");
  if (!fs.existsSync(client)) throw new Error("EVSign client was not found.");

  console.log(`[evsign] signing ${basename}`);
  const run = (command, args) => new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      stdio: ["ignore", "inherit", "inherit"], windowsHide: true,
    });
    child.on("error", reject);
    child.on("exit", code => code === 0 ? resolve() :
      reject(new Error(`EVSign failed for ${basename} with exit code ${code}.`)));
  });
  await run(client, [file, "-key", key, "-sha256"]);
  // Includes the temporary NSIS uninstaller before electron-builder embeds it.
  await run("powershell.exe", ["-NoProfile", "-NonInteractive", "-File",
    path.join(__dirname, "../scripts/verify-windows-signing.ps1"), "-SignedFile", file]);
}

module.exports = evsign;
module.exports.default = evsign;
