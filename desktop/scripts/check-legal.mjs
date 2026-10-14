import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const manifest = JSON.parse(fs.readFileSync(path.join(root, "third_party/licenses/manifest.json"), "utf8"));
for (const relative of ["LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "third_party/frp/LICENSE", "third_party/frp/REMOTE_CHANGES.md", "third_party/licenses/go.txt", "third_party/licenses/build-tools.txt", "third_party/licenses/electron.txt", "third_party/licenses/lucide.txt"]) {
  if (!fs.statSync(path.join(root, relative)).isFile()) throw new Error("Missing license resource: " + relative);
}
for (const [relative, expected] of Object.entries(manifest.dependencyFiles)) {
  const text = fs.readFileSync(path.join(root, relative), "utf8").replace(/\r\n/g, "\n").trimEnd() + "\n";
  const actual = crypto.createHash("sha256").update(text).digest("hex");
  if (actual !== expected) throw new Error("Review and update third-party notices after changing " + relative);
}
const electron = JSON.parse(fs.readFileSync(path.join(root, "desktop/node_modules/electron/package.json"), "utf8"));
if (electron.version !== manifest.electron.version) throw new Error("Electron license inventory version is stale");
console.log("License resources and dependency inventory verified");
