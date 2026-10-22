import { afterEach, describe, expect, it } from "vitest";
import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { gzipSync } from "node:zlib";
import { spawnSync } from "node:child_process";
import {
  makeProbe, releaseProbes, downloadManifestName, downloadResourceFiles,
  generateReleaseDownloadManifest, verifyReleaseDownloadManifest,
} from "../../scripts/generate-release-download-manifest.mjs";

const directories = [];
const tag = "desktop-v0.1.7";
const sha256 = bytes => crypto.createHash("sha256").update(bytes).digest("hex");
const harnessHashes = {
  65536: "030fa6ea296d12976742766f260befec0e4dfc3a4caae1a6137ddc03de551ffc",
  1048576: "6722eb9dcda67ea89af25514829a52b8d76fdb7a0cfa62f857c4c412d9528e12",
};
function fixture() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-download-manifest-test-"));
  directories.push(dir);
  generateReleaseDownloadManifest(dir, tag);
  return dir;
}
afterEach(() => {
  for (const dir of directories.splice(0)) {
    if (!path.resolve(dir).startsWith(path.join(os.tmpdir(), "remote-download-manifest-test-"))) throw new Error("Unsafe test cleanup path");
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

describe("release download probes compatible with Harness and Penguin Go", () => {
  it.each(releaseProbes)("generates deterministic, incompressible $label bytes matching Harness's v1 algorithm", probe => {
    const bytes = makeProbe(probe.size);
    expect(bytes.length).toBe(probe.size);
    expect(bytes.equals(makeProbe(probe.size))).toBe(true);
    expect(sha256(bytes)).toBe(harnessHashes[probe.size]);
    expect(gzipSync(bytes).length).toBeGreaterThan(probe.size * 0.99);
  });
  it("writes exactly the Go probe contract with real tabs, final LF, no BOM and hashes from disk", () => {
    const dir = fixture(), second = fixture();
    expect(fs.readdirSync(dir).sort()).toEqual([...downloadResourceFiles].sort());
    const bytes = fs.readFileSync(path.join(dir, downloadManifestName));
    expect(bytes.subarray(0, 3)).not.toEqual(Buffer.from([0xef, 0xbb, 0xbf]));
    const rows = bytes.toString("utf8").split("\n");
    expect(rows).toHaveLength(4);
    expect(rows[0].split("\t")).toEqual(["penguin-release-download-manifest", "1", tag]);
    expect(rows[3]).toBe("");
    for (const [index, probe] of releaseProbes.entries()) {
      const content = fs.readFileSync(path.join(dir, probe.file));
      expect(rows[index + 1].split("\t")).toEqual(["probe", probe.label, probe.file, String(content.length), sha256(content)]);
      expect(content.equals(fs.readFileSync(path.join(second, probe.file)))).toBe(true);
    }
    expect(() => verifyReleaseDownloadManifest(dir, tag)).not.toThrow();
  });
  it.each(downloadResourceFiles)("rejects missing resource %s", name => {
    const dir = fixture();
    fs.unlinkSync(path.join(dir, name));
    expect(() => verifyReleaseDownloadManifest(dir, tag)).toThrow();
  });
  it.each(["short", "long", "changed", "rehash-changed"])("rejects wrong probe contents: %s", mode => {
    const dir = fixture(), file = path.join(dir, "probe-64k.bin");
    let bytes = fs.readFileSync(file);
    if (mode === "short") bytes = bytes.subarray(1);
    else if (mode === "long") bytes = Buffer.concat([bytes, Buffer.from([0])]);
    else bytes[0] ^= 1;
    fs.writeFileSync(file, bytes);
    if (mode === "rehash-changed") {
      const manifest = path.join(dir, downloadManifestName);
      fs.writeFileSync(manifest, fs.readFileSync(manifest, "utf8").replace(harnessHashes[65536], sha256(bytes)));
    }
    expect(() => verifyReleaseDownloadManifest(dir, tag)).toThrow(/integrity mismatch/);
  });
  it.each([
    ["tag", text => text.replace(tag, "desktop-v0.2.0")],
    ["BOM", text => "\ufeff" + text],
    ["literal tabs", text => text.replaceAll("\t", "\\t")],
    ["missing LF", text => text.slice(0, -1)],
    ["identifier", text => text.replace("penguin-release-download-manifest", "other-manifest")],
    ["field order", text => text.replace("small\tprobe-64k.bin", "probe-64k.bin\tsmall")],
    ["size", text => text.replace("65536", "65535")],
    ["filename", text => text.replace("probe-64k.bin", "../probe-64k.bin")],
    ["duplicate label", text => text.replace("probe\tlarge", "probe\tsmall")],
    ["extra row", text => text + "probe\textra\textra.bin\t1\t" + "0".repeat(64) + "\n"],
    ["hash", text => text.replace(harnessHashes[65536], "0".repeat(64))],
    ["uppercase hash", text => text.replace(harnessHashes[65536], harnessHashes[65536].toUpperCase())],
  ])("rejects invalid manifest %s", (_name, edit) => {
    const dir = fixture(), file = path.join(dir, downloadManifestName);
    fs.writeFileSync(file, edit(fs.readFileSync(file, "utf8")));
    expect(() => verifyReleaseDownloadManifest(dir, tag)).toThrow();
  });
  it("supports the workflow CLI and rejects non-Client or unsafe tags before writing", () => {
    const dir = fixture();
    const script = path.resolve(import.meta.dirname, "../../scripts/generate-release-download-manifest.mjs");
    const result = spawnSync(process.execPath, [script, tag, dir], { encoding: "utf8" });
    expect(result.status, result.stderr).toBe(0);
    verifyReleaseDownloadManifest(dir, tag);
    for (const invalid of ["v0.1.7", "desktop-v0.1.7/evil", "desktop-v0.1.7-..", "desktop-v0.1.7-staging.01"]) {
      const destination = path.join(dir, "not-created");
      expect(() => generateReleaseDownloadManifest(destination, invalid)).toThrow();
      expect(fs.existsSync(destination)).toBe(false);
    }
  });
});
