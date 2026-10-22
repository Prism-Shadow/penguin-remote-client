import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const require = createRequire(new URL("../desktop/package.json", import.meta.url));
const yaml = require("yaml");
export const updateTargets = [
  { platform: "win", arch: "x64", feed: "latest.yml", asset: "penguin-remote-win-x64.exe" },
  { platform: "mac", arch: "x64", feed: "latest-mac.yml", asset: "penguin-remote-mac-x64.zip" },
  { platform: "mac", arch: "arm64", feed: "latest-mac.yml", asset: "penguin-remote-mac-arm64.zip" },
  { platform: "linux", arch: "x64", feed: "latest-linux.yml", asset: "penguin-remote-linux-x86_64.AppImage" },
];
export const requiredUpdateFeeds = [...new Set(updateTargets.map(target => target.feed))];
export const stagedMetadataName = target => `update-metadata-${target.platform}-${target.arch}.json`;

function digest(directory, name) {
  const bytes = fs.readFileSync(path.join(directory, name));
  if (!bytes.length) throw new Error("Empty update asset: " + name);
  return { sha512: crypto.createHash("sha512").update(bytes).digest("base64"), size: bytes.length };
}

/** Run after signing/notarization, before per-platform artifacts are uploaded. */
export function stageUpdateMetadata(directory, platform, arch, version) {
  const target = updateTargets.find(item => item.platform === platform && item.arch === arch);
  // Optional Linux arm64 manual builds are not in the formal release matrix yet.
  if (!target) throw new Error("Unsupported update target");
  const input = yaml.parse(fs.readFileSync(path.join(directory, target.feed), "utf8"));
  if (input.version !== version) throw new Error("Update metadata version mismatch");
  const matches = input.files?.filter(file => file.url === target.asset);
  if (matches?.length !== 1) throw new Error("Missing or duplicate update asset: " + target.asset);
  const file = { ...matches[0], ...digest(directory, target.asset) };
  // Names are unique across architecture jobs; latest-mac.yml must not be overwritten
  // when Actions merges the independently built x64 and arm64 artifact directories.
  fs.writeFileSync(path.join(directory, stagedMetadataName(target)), JSON.stringify({ version, files: [file], minimumSystemVersion: input.minimumSystemVersion, releaseDate: input.releaseDate }, null, 2) + "\n");
}

/** Combine architecture snapshots only after the complete release matrix arrives. */
export function mergeUpdateMetadata(directory, version) {
  for (const feed of requiredUpdateFeeds) {
    const files = [];
    let metadata;
    for (const target of updateTargets.filter(item => item.feed === feed)) {
      const input = JSON.parse(fs.readFileSync(path.join(directory, stagedMetadataName(target)), "utf8"));
      if (input.version !== version || input.files?.length !== 1 || input.files[0].url !== target.asset) throw new Error("Invalid staged update metadata");
      if (metadata && metadata.minimumSystemVersion !== input.minimumSystemVersion) throw new Error("Architecture system requirements differ");
      metadata ??= input;
      const file = input.files[0], actual = digest(directory, target.asset);
      if (file.sha512 !== actual.sha512 || file.size !== actual.size) throw new Error("Update integrity mismatch: " + target.asset);
      files.push(file);
    }
    fs.writeFileSync(path.join(directory, feed), yaml.stringify({ version, files, path: files[0].url, sha512: files[0].sha512, minimumSystemVersion: metadata.minimumSystemVersion, releaseDate: metadata.releaseDate }));
  }
  for (const target of updateTargets) fs.unlinkSync(path.join(directory, stagedMetadataName(target)));
}

export function verifyUpdateMetadata(directory, version) {
  for (const feed of requiredUpdateFeeds) {
    const input = yaml.parse(fs.readFileSync(path.join(directory, feed), "utf8"));
    const targets = updateTargets.filter(item => item.feed === feed);
    if (input.version !== version || input.files?.length !== targets.length) throw new Error("Incomplete update feed: " + feed);
    for (const target of targets) {
      const matches = input.files.filter(file => file.url === target.asset);
      const actual = digest(directory, target.asset);
      if (matches.length !== 1 || matches[0].sha512 !== actual.sha512 || matches[0].size !== actual.size) throw new Error("Update integrity mismatch: " + target.asset);
    }
    if (input.path !== input.files[0].url || input.sha512 !== input.files[0].sha512) throw new Error("Invalid legacy update metadata: " + feed);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const { version } = JSON.parse(fs.readFileSync(path.join(root, "desktop/package.json"), "utf8"));
  const [action, directory, platform, arch] = process.argv.slice(2);
  if (action === "stage") stageUpdateMetadata(directory, platform, arch, version);
  else if (action === "merge") mergeUpdateMetadata(directory, version);
  else throw new Error("Expected stage or merge");
}
