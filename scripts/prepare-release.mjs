import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";
import { productionGoUrl, releaseTarget } from "./release-target.mjs";

export const requiredAssets = [
  "penguin-remote-win-x64.exe",
  "penguin-remote-mac-x64.dmg",
  "penguin-remote-mac-x64.zip",
  "penguin-remote-mac-arm64.dmg",
  "penguin-remote-mac-arm64.zip",
  "penguin-remote-linux-x86_64.AppImage",
  "penguin-remote-linux-amd64.deb",
];

export const signingTargets = [
  { report: "signing-windows-x64.json", platform: "win", arch: "x64", notarized: false, assets: [requiredAssets[0]] },
  { report: "signing-macos-x64.json", platform: "mac", arch: "x64", notarized: true, assets: requiredAssets.slice(1, 3) },
  { report: "signing-macos-arm64.json", platform: "mac", arch: "arm64", notarized: true, assets: requiredAssets.slice(3, 5) },
];

export function verifySigningReports(directory, commit, penguinGoUrl = productionGoUrl) {
  for (const target of signingTargets) {
    const report = JSON.parse(fs.readFileSync(path.join(directory, target.report), "utf8"));
    if (report.platform !== target.platform || report.arch !== target.arch || report.sourceCommit !== commit ||
        report.trustedCodeSigning !== true || report.notarized !== target.notarized ||
        report.penguinGoUrl !== penguinGoUrl) {
      throw new Error("Invalid signing verification report: " + target.report);
    }
    if (!Array.isArray(report.assets) || report.assets.length !== target.assets.length) {
      throw new Error("Incomplete signing verification report: " + target.report);
    }
    for (const name of target.assets) {
      const matches = report.assets.filter(asset => asset.name === name);
      const digest = crypto.createHash("sha256").update(fs.readFileSync(path.join(directory, name))).digest("hex");
      if (matches.length !== 1 || matches[0].sha256 !== digest) {
        throw new Error("Signed release asset integrity mismatch: " + name);
      }
    }
  }
}

export function prepareRelease(directory, tag, commit, version) {
  const release = releaseTarget(version);
  if (tag !== "desktop-v" + version || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error("Release tag/package version mismatch");
  if (!/^[0-9a-f]{40}$/.test(commit)) throw new Error("Release source must be a full commit SHA");
  const allowed = new Set([...requiredAssets, ...requiredAssets.map(file => file + ".blockmap"),
    ...signingTargets.map(target => target.report)]);
  const files = fs.readdirSync(directory, {withFileTypes:true});
  for (const entry of files) if (!entry.isFile() || !allowed.has(entry.name)) throw new Error("Unexpected release input: " + entry.name);
  for (const file of requiredAssets) if (!fs.existsSync(path.join(directory,file))) throw new Error("Missing release asset: " + file);
  // Reports are emitted only after platform signature/notarization verification.
  verifySigningReports(directory, commit, release.penguinGoUrl);
  const assets = files.map(entry => {
    const bytes = fs.readFileSync(path.join(directory,entry.name));
    if (!bytes.length) throw new Error("Empty release asset: " + entry.name);
    return {name:entry.name, size:bytes.length, sha256:crypto.createHash("sha256").update(bytes).digest("hex")};
  }).sort((a,b) => a.name.localeCompare(b.name));
  fs.writeFileSync(path.join(directory,"SHA256SUMS.txt"), assets.map(asset => asset.sha256 + "  " + asset.name).join("\n") + "\n");
  fs.writeFileSync(path.join(directory,"release-manifest.json"), JSON.stringify({version, tag, sourceCommit:commit, releaseChannel:release.channel, penguinGoUrl:release.penguinGoUrl, trustedCodeSigning:true, notarized:true, assets}, null, 2) + "\n");
  return assets;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const {values} = parseArgs({options:{dir:{type:"string"},tag:{type:"string"},commit:{type:"string"}}, strict:true});
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const metadata = JSON.parse(fs.readFileSync(path.join(root,"desktop/package.json"),"utf8"));
  const assets = prepareRelease(values.dir, values.tag, values.commit, metadata.version);
  console.log("Release complete: " + assets.length + " assets verified for " + values.tag);
}
