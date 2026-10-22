import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { githubClient } from "./oss-release-tools.mjs";
import { verifyPublicRelease, verifyReleaseMirror } from "./verify-release-mirror.mjs";

export function downloadReleaseMirror(directory, tag, github = githubClient()) {
  const release = github.release(tag);
  verifyPublicRelease(release, tag);
  // A dedicated empty directory prevents accepting leftover downloads from another run.
  fs.mkdirSync(directory);
  github.download(tag, directory);
  return verifyReleaseMirror(directory, release, tag);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  downloadReleaseMirror(process.argv[2], process.argv[3]);
  console.log("Published GitHub Release downloaded and verified");
}
