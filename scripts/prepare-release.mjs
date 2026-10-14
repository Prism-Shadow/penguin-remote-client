import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { parseArgs } from "node:util";
import { fileURLToPath } from "node:url";

export const requiredAssets = [
  "penguin-remote-win-x64.exe",
  "penguin-remote-mac-x64.dmg",
  "penguin-remote-mac-x64.zip",
  "penguin-remote-mac-arm64.dmg",
  "penguin-remote-mac-arm64.zip",
  "penguin-remote-linux-x86_64.AppImage",
  "penguin-remote-linux-amd64.deb",
];

export function prepareRelease(directory, tag, commit, version) {
  if (tag !== "desktop-v" + version || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error("Release tag/package version mismatch");
  if (!/^[0-9a-f]{40}$/.test(commit)) throw new Error("Release source must be a full commit SHA");
  const allowed = new Set([...requiredAssets, ...requiredAssets.map(file => file + ".blockmap")]);
  const files = fs.readdirSync(directory, {withFileTypes:true});
  for (const entry of files) if (!entry.isFile() || !allowed.has(entry.name)) throw new Error("Unexpected release input: " + entry.name);
  for (const file of requiredAssets) if (!fs.existsSync(path.join(directory,file))) throw new Error("Missing release asset: " + file);
  const assets = files.map(entry => {
    const bytes = fs.readFileSync(path.join(directory,entry.name));
    if (!bytes.length) throw new Error("Empty release asset: " + entry.name);
    return {name:entry.name, size:bytes.length, sha256:crypto.createHash("sha256").update(bytes).digest("hex")};
  }).sort((a,b) => a.name.localeCompare(b.name));
  fs.writeFileSync(path.join(directory,"SHA256SUMS.txt"), assets.map(asset => asset.sha256 + "  " + asset.name).join("\n") + "\n");
  fs.writeFileSync(path.join(directory,"release-manifest.json"), JSON.stringify({version, tag, sourceCommit:commit, penguinGoUrl:"https://go.penguin.ooo", trustedCodeSigning:false, notarized:false, assets}, null, 2) + "\n");
  return assets;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const {values} = parseArgs({options:{dir:{type:"string"},tag:{type:"string"},commit:{type:"string"}}, strict:true});
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const metadata = JSON.parse(fs.readFileSync(path.join(root,"desktop/package.json"),"utf8"));
  const assets = prepareRelease(values.dir, values.tag, values.commit, metadata.version);
  console.log("Release complete: " + assets.length + " assets verified for " + values.tag);
}
