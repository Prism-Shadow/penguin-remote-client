import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";

export function verifyUploadedRelease(directory, release) {
  if (!release.draft || !release.prerelease) throw new Error("Upload verification must run on an unpublished prerelease");
  const manifest = JSON.parse(fs.readFileSync(path.join(directory,"release-manifest.json"),"utf8"));
  if (release.tag_name !== manifest.tag || release.target_commitish !== manifest.sourceCommit) throw new Error("Release source/tag mismatch");
  const files = fs.readdirSync(directory);
  if (!Array.isArray(release.assets) || release.assets.length !== files.length) throw new Error("Uploaded asset list is incomplete or unexpected");
  for (const file of files) {
    const uploaded = release.assets.filter(asset => asset.name === file);
    const bytes = fs.readFileSync(path.join(directory,file));
    const digest = "sha256:" + crypto.createHash("sha256").update(bytes).digest("hex");
    if (uploaded.length !== 1 || uploaded[0].size !== bytes.length || uploaded[0].digest !== digest) throw new Error("Uploaded asset integrity mismatch: " + file);
  }
  console.log("All uploaded draft release assets match their local SHA-256 digests");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  verifyUploadedRelease(process.argv[2],JSON.parse(fs.readFileSync(process.argv[3],"utf8")));
}
