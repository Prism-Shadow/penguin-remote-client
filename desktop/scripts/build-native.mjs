import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const desktopDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = path.resolve(desktopDir, "..");

function fail(message) {
  console.error(message);
  process.exit(1);
}

function hostTarget() {
  const goos = { win32: "windows", darwin: "darwin", linux: "linux" }[process.platform];
  const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!goos || !goarch) fail("Unsupported host: " + process.platform + "/" + process.arch);
  return { goos, goarch };
}

export function buildNative(platform, arch) {
  const goos =
    platform === "host"
      ? hostTarget().goos
      : { win: "windows", mac: "darwin", linux: "linux" }[platform];
  const goarch =
    platform === "host"
      ? hostTarget().goarch
      : { x64: "amd64", arm64: "arm64" }[arch];
  if (!goos || !goarch) fail("Unsupported target: " + platform + "/" + arch);

  const nativeDir = path.join(desktopDir, "native");
  fs.rmSync(nativeDir, { recursive: true, force: true });
  fs.mkdirSync(nativeDir, { recursive: true });
  const output = path.join(
    nativeDir,
    goos === "windows" ? "penguin-remote.exe" : "penguin-remote",
  );
  const result = spawnSync(
    "go",
    ["build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", output, "./cmd/penguin-remote"],
    {
      cwd: repoRoot,
      env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: "0" },
      encoding: "utf8",
      stdio: "inherit",
    },
  );
  if (result.status !== 0) fail("Go build failed for " + goos + "/" + goarch);
  if (goos !== "windows") fs.chmodSync(output, 0o755);
  console.log("Built " + path.relative(repoRoot, output) + " for " + goos + "/" + goarch);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const platform = process.argv[2] ?? "host";
  const arch = process.argv[3] ?? (os.arch() === "arm64" ? "arm64" : "x64");
  buildNative(platform, arch);
}
