import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { requiredAssets, signingTargets, verifySigningReports } from "./prepare-release.mjs";
import { releaseTarget, releaseVersion } from "./release-target.mjs";
export { releaseVersion } from "./release-target.mjs";
import { requiredUpdateFeeds, verifyUpdateMetadata } from "./update-metadata.mjs";
import { downloadResourceFiles, verifyReleaseDownloadManifest } from "./generate-release-download-manifest.mjs";

export const sha256 = bytes => crypto.createHash("sha256").update(bytes).digest("hex");

// Audited historical tag sources define the only exceptions to the current contract.
// Pin their commits so a replaced tag cannot reuse an older, weaker asset contract.
const historicalContracts = new Map([
  ["desktop-v0.1.0", { sourceCommit: "47ae323454abb7558f980676237879a03c104478", signed: false, legacyLinux: true }],
  ["desktop-v0.1.1", { sourceCommit: "d120c81641c76cc40a240f92980e6ce6ee5b61c3", signed: false }],
  ["desktop-v0.1.2", { sourceCommit: "9130258e82acd219c5d1ee2ad2e0334434fc53e1", signed: false }],
  ["desktop-v0.1.3-staging.1", { sourceCommit: "e5b30be662bc3df1297976907067d52e1b2db8d2", signed: true }],
  ["desktop-v0.1.3", { sourceCommit: "de6b84e0a0d87bbf5a2c7dafcfd2013dbdccce79", signed: true }],
  ["desktop-v0.1.4", { sourceCommit: "8897e13300f87effc00c9fab4e837a33313fce79", signed: true }],
  ["desktop-v0.1.5", { sourceCommit: "98edd11798b054f3cde5f131a25149289cd538d6", signed: true }],
  ["desktop-v0.1.6", { sourceCommit: "147d71a22aea87e2b1abb0807e48c73c4d4f4f45", signed: true, feeds: true }],
]);

function assetContract(tag, sourceCommit) {
  const historical = historicalContracts.get(tag);
  if (!historical) return { signed: true, feeds: true, probes: true, legacyLinux: false };
  if (historical.sourceCommit !== sourceCommit) throw new Error("Historical release sourceCommit mismatch: " + tag);
  return { ...historical, feeds: historical.feeds ?? false, probes: false, legacyLinux: historical.legacyLinux ?? false };
}

export function verifyPublicRelease(release, tag) {
  releaseVersion(tag);
  if (!Number.isSafeInteger(release.id) || release.id <= 0 || release.tag_name !== tag || release.draft !== false ||
      typeof release.prerelease !== "boolean" || !release.published_at) {
    throw new Error("Mirror requires an existing published GitHub Release with the requested tag");
  }
  if (!Array.isArray(release.assets) || !release.assets.length) throw new Error("Missing GitHub Release assets");
  const names = new Set();
  for (const asset of release.assets) {
    if (typeof asset.name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._+-]*$/.test(asset.name) || asset.name.includes("..") || names.has(asset.name)) {
      throw new Error("Unsafe or duplicate GitHub asset name");
    }
    if (!Number.isSafeInteger(asset.size) || asset.size <= 0 || !/^sha256:[0-9a-f]{64}$/.test(asset.digest)) {
      throw new Error("GitHub asset requires a positive size and SHA-256 digest: " + asset.name);
    }
    names.add(asset.name);
  }
}

/** Read the published manifest; never regenerate historical metadata from package.json. */
export function verifyReleaseMirror(directory, release, tag) {
  verifyPublicRelease(release, tag);
  const entries = fs.readdirSync(directory, { withFileTypes: true });
  const names = entries.map(entry => entry.name);
  if (entries.some(entry => !entry.isFile()) || names.length !== release.assets.length) throw new Error("Downloaded asset set differs from GitHub");
  // Includes both metadata files, which cannot hash themselves in the manifest.
  for (const name of names) {
    const asset = release.assets.find(item => item.name === name);
    const bytes = fs.readFileSync(path.join(directory, name));
    if (!asset || asset.size !== bytes.length || asset.digest !== "sha256:" + sha256(bytes)) throw new Error("GitHub asset integrity mismatch: " + name);
  }
  const manifest = JSON.parse(fs.readFileSync(path.join(directory, "release-manifest.json"), "utf8"));
  const version = releaseVersion(tag), target = releaseTarget(version);
  if (manifest.tag !== tag || manifest.version !== version || !/^[0-9a-f]{40}$/.test(manifest.sourceCommit) ||
      release.target_commitish !== manifest.sourceCommit) throw new Error("Manifest tag/version/sourceCommit mismatch");
  const contract = assetContract(tag, manifest.sourceCommit);
  if (manifest.penguinGoUrl !== target.penguinGoUrl || (manifest.releaseChannel !== undefined && manifest.releaseChannel !== target.channel)) {
    throw new Error("Manifest release channel mismatch");
  }
  if (!Array.isArray(manifest.assets) || !manifest.assets.length) throw new Error("Missing manifest assets");
  const listed = new Set();
  for (const asset of manifest.assets) {
    if (!names.includes(asset.name) || listed.has(asset.name) || ["release-manifest.json", "SHA256SUMS.txt"].includes(asset.name)) {
      throw new Error("Missing, duplicate or unexpected manifest asset");
    }
    const bytes = fs.readFileSync(path.join(directory, asset.name));
    if (asset.size !== bytes.length || asset.sha256 !== sha256(bytes)) throw new Error("Manifest asset integrity mismatch: " + asset.name);
    listed.add(asset.name);
  }
  if (names.length !== listed.size + 2 || !names.includes("SHA256SUMS.txt")) throw new Error("Manifest asset set is incomplete");
  const sums = fs.readFileSync(path.join(directory, "SHA256SUMS.txt"), "utf8").trimEnd().split(/\r?\n/);
  const checked = new Set();
  for (const line of sums) {
    const match = /^([0-9a-f]{64})  (.+)$/.exec(line);
    if (!match || !listed.has(match[2]) || checked.has(match[2]) || manifest.assets.find(asset => asset.name === match[2]).sha256 !== match[1]) {
      throw new Error("SHA256SUMS asset integrity/set mismatch");
    }
    checked.add(match[2]);
  }
  if (checked.size !== listed.size) throw new Error("SHA256SUMS is incomplete");

  // The trusted tag/source contract determines requirements, never missing files
  // or signing declarations supplied by the manifest being verified.
  const installers = requiredAssets.map(name => contract.legacyLinux ?
    name.replace("linux-x86_64", "linux-x64").replace("linux-amd64", "linux-x64") : name);
  const feeds = contract.feeds ? requiredUpdateFeeds : [];
  const reports = contract.signed ? signingTargets.map(target => target.report) : [];
  const probes = contract.probes ? downloadResourceFiles : [];
  if (manifest.trustedCodeSigning !== contract.signed || manifest.notarized !== contract.signed) {
    throw new Error("Release signing contract mismatch: " + tag);
  }
  for (const name of [...installers, ...reports, ...feeds, ...probes]) {
    if (!listed.has(name)) throw new Error("Missing required Client release asset: " + name);
  }
  const allowed = new Set([...installers, ...installers.map(name => name + ".blockmap"), ...reports, ...feeds, ...probes]);
  for (const name of listed) if (!allowed.has(name)) throw new Error("Unknown Client release asset: " + name);
  if (contract.signed) {
    verifySigningReports(directory, manifest.sourceCommit, target.penguinGoUrl);
  }
  if (contract.feeds) verifyUpdateMetadata(directory, version);
  if (contract.probes) verifyReleaseDownloadManifest(directory, tag);
  return { manifest, files: names.sort() };
}
