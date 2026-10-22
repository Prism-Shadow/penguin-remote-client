import { afterEach, describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import yaml from "yaml";
import { mergeUpdateMetadata, stagedMetadataName, stageUpdateMetadata, updateTargets, verifyUpdateMetadata } from "../../scripts/update-metadata.mjs";

const directories = [];
function fixture() {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "remote-update-metadata-"));
  directories.push(directory);
  for (const target of updateTargets) {
    fs.writeFileSync(path.join(directory, target.asset), "final-signed-bytes-" + target.asset);
    fs.writeFileSync(path.join(directory, target.feed), yaml.stringify({ version:"0.1.6", minimumSystemVersion:"10.0.0", files:[{url:target.asset,sha512:"before-signing",size:1}, {url:"old.dmg",sha512:"stale"}] }));
    stageUpdateMetadata(directory, target.platform, target.arch, "0.1.6");
  }
  return directory;
}
afterEach(() => {
  for (const directory of directories.splice(0)) {
    if (!directory.startsWith(path.join(os.tmpdir(), "remote-update-metadata-"))) throw new Error("Unsafe cleanup path");
    fs.rmSync(directory, {recursive:true,force:true});
  }
});

describe("release updater metadata", () => {
  it("merges both macOS architectures and binds checksums to final bytes", () => {
    const directory = fixture();
    mergeUpdateMetadata(directory, "0.1.6");
    verifyUpdateMetadata(directory, "0.1.6");
    const mac = yaml.parse(fs.readFileSync(path.join(directory, "latest-mac.yml"), "utf8"));
    expect(mac.files.map(file => file.url)).toEqual(["penguin-remote-mac-x64.zip", "penguin-remote-mac-arm64.zip"]);
    expect(mac.files[0].sha512).not.toBe("before-signing");
    expect(mac.minimumSystemVersion).toBe("10.0.0");
    expect(fs.readFileSync(path.join(directory, "latest-mac.yml"), "utf8")).not.toContain(".dmg");
    expect(fs.readdirSync(directory).some(name => name.startsWith("update-metadata-"))).toBe(false);
    fs.appendFileSync(path.join(directory, updateTargets[2].asset), "tampered");
    expect(() => verifyUpdateMetadata(directory, "0.1.6")).toThrow(/integrity/);
  });

  it("blocks missing architecture snapshots, mismatched versions and changed artifacts", () => {
    const directory = fixture();
    const snapshot = path.join(directory, stagedMetadataName(updateTargets[2]));
    const original = fs.readFileSync(snapshot, "utf8");
    fs.unlinkSync(snapshot);
    expect(() => mergeUpdateMetadata(directory, "0.1.6")).toThrow();
    fs.writeFileSync(snapshot, original.replace('"0.1.6"', '"0.1.7"'));
    expect(() => mergeUpdateMetadata(directory, "0.1.6")).toThrow();
    fs.writeFileSync(snapshot, original);
    fs.appendFileSync(path.join(directory, updateTargets[0].asset), "changed");
    expect(() => mergeUpdateMetadata(directory, "0.1.6")).toThrow(/integrity/);
  });

  it("rejects metadata for another architecture or an unexpected download URL", () => {
    const directory = fixture();
    expect(() => stageUpdateMetadata(directory, "mac", "x64", "0.1.6")).toThrow(/Missing/);
    fs.writeFileSync(path.join(directory, "latest.yml"), yaml.stringify({version:"0.1.6",files:[{url:"https://evil.example/installer.exe"}]}));
    expect(() => stageUpdateMetadata(directory, "win", "x64", "0.1.6")).toThrow(/Missing/);
    expect(() => stageUpdateMetadata(directory, "win", "x64", "0.1.7")).toThrow(/version/);
  });
});
