import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { githubClient, ossClient, ossConfig, sameBytes } from "./oss-release-tools.mjs";
import { verifyReleaseMirror } from "./verify-release-mirror.mjs";
import { releaseTarget } from "./release-target.mjs";

export function mirrorRelease({ directory, tag, github, oss, config, log = console.log }) {
  // Refresh GitHub metadata after download and before the first OSS mutation.
  const release = github.release(tag);
  const { manifest, files } = verifyReleaseMirror(directory, release, tag);
  const eligible = !release.prerelease && releaseTarget(manifest.version).makeLatest;
  const initiallyLatest = eligible && github.latest()?.tag_name === tag;
  for (const name of files) {
    const key = `releases/${tag}/${name}`, bytes = fs.readFileSync(path.join(directory, name));
    const existing = oss.get(key);
    if (existing !== null) {
      sameBytes(existing, bytes, key);
      log("Already mirrored: " + key);
      continue;
    }
    let uploadError;
    try { oss.put(key, bytes, { immutable: true, cache: "public,max-age=31536000,immutable" }); }
    catch (error) { uploadError = error; }
    // A create race or an ambiguous upload failure is successful only if the final bytes match.
    try { sameBytes(oss.get(key), bytes, key); }
    catch (error) { throw new Error(error.message, { cause: uploadError ?? error }); }
    log("Verified: " + key);
  }
  let updatedLatest = false;
  if (initiallyLatest) {
    // All entry points hold the same workflow concurrency group through this final check/write.
    const current = github.release(tag), latest = github.latest();
    if (current.draft === false && current.prerelease === false && current.id === release.id &&
        latest?.draft === false && latest.prerelease === false && latest.tag_name === tag && latest.id === release.id) {
      const bytes = Buffer.from(JSON.stringify({ schemaVersion: 1, tag, version: manifest.version,
        releaseBaseUrl: `${config.publicBase}/releases/${tag}` }, null, 2) + "\n");
      oss.put("latest.json", bytes, { cache: "no-cache" });
      sameBytes(oss.get("latest.json"), bytes, "latest.json");
      updatedLatest = true;
    }
  }
  log(updatedLatest ? "latest.json verified" : "latest.json unchanged: prerelease or not GitHub current Latest");
  return { files, updatedLatest };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "remote-oss-mirror-"));
  try {
    const config = ossConfig();
    mirrorRelease({ directory: process.argv[2], tag: process.argv[3], github: githubClient(), oss: ossClient(config, scratch), config });
  } finally {
    if (!scratch.startsWith(path.join(os.tmpdir(), "remote-oss-mirror-"))) throw new Error("Unsafe cleanup path");
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
