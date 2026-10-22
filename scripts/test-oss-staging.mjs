import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";
import { ossClient, ossConfig, sameBytes } from "./oss-release-tools.mjs";

function expectDenied(operation, key) {
  try { operation(); }
  catch (error) {
    if (/AccessDenied|Forbidden|\b403\b/i.test(error.message)) return;
    throw new Error("Permission probe failed without AccessDenied: " + key, { cause: error });
  }
  throw new Error("Staging role unexpectedly wrote production object: " + key);
}

export async function testOssStaging({ oss, config, fetchPublic = fetch, log = console.log, run = crypto.randomUUID() }) {
  if (!/^[A-Za-z0-9_-]+$/.test(run)) throw new Error("Invalid staging run identifier");
  const bytes = Buffer.from(`Penguin Remote staging round trip: ${run}\n`);
  const key = `staging/${run}/oidc-probe.txt`;
  oss.put(key, bytes, { immutable: true, cache: "no-cache" });
  sameBytes(oss.get(key), bytes, key);
  log("PASS: staging upload/download content verified");
  const deniedKey = `releases/_staging-deny-probe/${run}.txt`;
  expectDenied(() => oss.put(deniedKey, bytes, { immutable: true }), deniedKey);
  log("PASS: releases/ write denied");

  // The staging role cannot GetObject on production keys. Fetch an existing public
  // pointer first; never create a fake production pointer just to probe permissions.
  const response = await fetchPublic(`${config.publicBase}/latest.json`, { cache: "no-store" });
  if (response.status === 404) {
    log("PENDING: latest.json write-denial probe NOT EXECUTED; repeat after a real stable mirror creates latest.json");
    return { latestProbe: "pending", staging: "passed", releasesProbe: "passed" };
  }
  if (!response.ok) throw new Error("Public latest.json GET failed: HTTP " + response.status);
  const latest = Buffer.from(await response.arrayBuffer());
  if (!latest.length) throw new Error("Public latest.json is empty");
  expectDenied(() => oss.put("latest.json", latest, { immutable: true }), "latest.json");
  log("PASS: latest.json write denied (forbid-overwrite protected the existing object)");
  return { latestProbe: "passed", staging: "passed", releasesProbe: "passed" };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "remote-oss-staging-"));
  try {
    const config = ossConfig();
    const messages = [];
    await testOssStaging({ oss: ossClient(config, scratch), config, log: message => { messages.push(message); console.log(message); } });
    if (process.env.GITHUB_STEP_SUMMARY) fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, messages.join("\n\n") + "\n");
  } finally {
    if (!scratch.startsWith(path.join(os.tmpdir(), "remote-oss-staging-"))) throw new Error("Unsafe cleanup path");
    fs.rmSync(scratch, { recursive: true, force: true });
  }
}
