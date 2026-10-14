import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";
import { signingTargets } from "../../scripts/prepare-release.mjs";

const [platform, arch] = process.argv.slice(2);
const target = signingTargets.find(candidate => candidate.platform === platform && candidate.arch === arch);
if (!target) throw new Error("Unsupported signing verification target");
const required = platform === "win" ? process.env.REQUIRE_WINDOWS_SIGNING : process.env.REQUIRE_MACOS_SIGNING;
if (required !== "true") throw new Error("Signing verification reports require a signed build");
if (!/^[0-9a-f]{40}$/.test(process.env.GITHUB_SHA || "")) throw new Error("Signing report requires the full source commit SHA");
const outDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../dist/desktop");
const assets = target.assets.map(name => {
  const bytes = fs.readFileSync(path.join(outDir, name));
  if (!bytes.length) throw new Error("Empty signed asset: " + name);
  return { name, sha256: crypto.createHash("sha256").update(bytes).digest("hex") };
});
fs.writeFileSync(path.join(outDir, target.report), JSON.stringify({
  platform, arch, sourceCommit: process.env.GITHUB_SHA, trustedCodeSigning: true,
  notarized: platform === "mac", assets,
}, null, 2) + "\n");
