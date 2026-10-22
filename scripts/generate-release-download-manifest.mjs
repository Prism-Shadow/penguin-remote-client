import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { releaseVersion } from "./release-target.mjs";

export const releaseProbes = [
  { label: "small", file: "probe-64k.bin", size: 65536 },
  { label: "large", file: "probe-1m.bin", size: 1048576 },
];
export const downloadManifestName = "release-download-manifest.tsv";
export const downloadResourceFiles = [...releaseProbes.map(probe => probe.file), downloadManifestName];
const sha256 = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const canonicalHashes = new Map();

// Keep the Harness v1 seed and block algorithm so both projects produce identical bytes.
export function makeProbe(size) {
  if (!Number.isSafeInteger(size) || size <= 0 || size > 1048576) throw new Error("Invalid probe size");
  const out = Buffer.allocUnsafe(size);
  let offset = 0;
  let counter = 0;
  while (offset < size) {
    const chunk = crypto.createHash("sha256")
      .update("penguin-harness release probe v1\0")
      .update(String(size))
      .update("\0")
      .update(String(counter))
      .digest();
    counter += 1;
    offset += chunk.copy(out, offset, 0, Math.min(chunk.length, size - offset));
  }
  return out;
}

export function generateReleaseDownloadManifest(directory, tag) {
  releaseVersion(tag);
  fs.mkdirSync(directory, { recursive: true });
  const rows = [["penguin-release-download-manifest", "1", tag]];
  for (const probe of releaseProbes) {
    const file = path.join(directory, probe.file);
    fs.writeFileSync(file, makeProbe(probe.size));
    const bytes = fs.readFileSync(file);
    rows.push(["probe", probe.label, probe.file, String(bytes.length), sha256(bytes)]);
  }
  fs.writeFileSync(path.join(directory, downloadManifestName), rows.map(row => row.join("\t")).join("\n") + "\n", "utf8");
}

export function verifyReleaseDownloadManifest(directory, tag) {
  releaseVersion(tag);
  for (const name of downloadResourceFiles) {
    if (!fs.lstatSync(path.join(directory, name)).isFile()) throw new Error("Invalid download resource: " + name);
  }
  const text = fs.readFileSync(path.join(directory, downloadManifestName), "utf8");
  const lines = text.split("\n");
  if (lines.length !== 4 || lines[3] !== "" || lines[0] !== `penguin-release-download-manifest\t1\t${tag}`) {
    throw new Error("Invalid release download manifest header/tag/format");
  }
  for (const [index, probe] of releaseProbes.entries()) {
    const fields = lines[index + 1].split("\t");
    if (fields.length !== 5 || fields[0] !== "probe" || fields[1] !== probe.label || fields[2] !== probe.file ||
        fields[3] !== String(probe.size) || !/^[0-9a-f]{64}$/.test(fields[4])) {
      throw new Error("Invalid release download probe row: " + probe.label);
    }
    const bytes = fs.readFileSync(path.join(directory, probe.file));
    if (!canonicalHashes.has(probe.size)) canonicalHashes.set(probe.size, sha256(makeProbe(probe.size)));
    const hash = sha256(bytes);
    if (bytes.length !== probe.size || hash !== fields[4] || hash !== canonicalHashes.get(probe.size)) {
      throw new Error("Release download probe integrity mismatch: " + probe.file);
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const [tag, directory, ...extra] = process.argv.slice(2);
  if (!tag || !directory || extra.length) throw new Error("usage: generate-release-download-manifest.mjs <tag> <output-dir>");
  generateReleaseDownloadManifest(directory, tag);
  verifyReleaseDownloadManifest(directory, tag);
  console.log("Generated and verified release download probes for " + tag);
}
