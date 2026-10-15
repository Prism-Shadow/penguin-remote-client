import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { releaseTarget } from "../../scripts/release-target.mjs";
import nativeIntegrity from "../build/native-integrity.cjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

export function resourcesDirectory(platform, arch) {
  if (platform === "win" && arch === "x64") return "win-unpacked/resources";
  if (platform === "linux" && arch === "x64") return "linux-unpacked/resources";
  if (platform === "linux" && arch === "arm64") return "linux-arm64-unpacked/resources";
  if (platform === "mac" && ["x64", "arm64"].includes(arch)) {
    return (arch === "arm64" ? "mac-arm64" : "mac") + "/Penguin Remote.app/Contents/Resources";
  }
  throw new Error("Unsupported package target");
}

export function verifyPackage(platform, arch) {
  const resources = path.join(root, "dist/desktop", resourcesDirectory(platform, arch));
  const require = createRequire(import.meta.url);
  const builderRequire = createRequire(require.resolve("electron-builder"));
  const appBuilderRequire = createRequire(builderRequire.resolve("app-builder-lib"));
  const asar = appBuilderRequire("@electron/asar");
  const config = JSON.parse(asar.extractFile(path.join(resources, "app.asar"), "dist/build-config.json").toString());
  const metadata = JSON.parse(fs.readFileSync(path.join(root, "desktop/package.json"), "utf8"));
  const expectedURL = (process.env.PENGUIN_REMOTE_GO_URL || releaseTarget(metadata.version).penguinGoUrl).trim();
  if (config.penguinGoUrl !== expectedURL) throw new Error("Packaged service address differs from build target");

  const files = ["LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "third_party/frp/LICENSE", "third_party/frp/REMOTE_CHANGES.md"];
  for (const entry of fs.readdirSync(path.join(root, "third_party/licenses"), { withFileTypes: true })) {
    if (entry.isFile()) files.push("third_party/licenses/" + entry.name);
  }
  for (const relative of files) {
    if (!fs.readFileSync(path.join(root, relative)).equals(fs.readFileSync(path.join(resources, "legal", relative)))) {
      throw new Error("Packaged legal resource differs: " + relative);
    }
  }
  const licenseRoot = platform === "mac" ? resources : path.dirname(resources);
  for (const file of ["LICENSE.electron.txt", "LICENSES.chromium.html"]) {
    if (fs.statSync(path.join(licenseRoot, file)).size === 0) throw new Error("Empty Electron runtime notice");
  }

  const binaryName = platform === "win" ? "penguin-remote.exe" : "penguin-remote";
  const binary = path.join(resources, "bin", binaryName);
  const appOutDir = platform === "mac" ? path.resolve(resources, "../../..") : path.dirname(resources);
  nativeIntegrity.verifyNativeIntegrity(
    path.join(root, "desktop/native", binaryName), binary, path.join(appOutDir, ".native-integrity.json"),
  );
  const host = {win32:"win", darwin:"mac", linux:"linux"}[process.platform];
  if (host === platform && process.arch === arch) {
    const help = spawnSync(binary, ["help"], {encoding:"utf8", timeout:15_000});
    if (help.status !== 0 || !help.stdout.includes("Penguin Remote")) throw new Error("Bundled native client failed its help smoke test");
  }
  console.log("Verified " + platform + "/" + arch + " package, service address, native binary and license resources");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  verifyPackage(process.argv[2], process.argv[3] || "x64");
}
