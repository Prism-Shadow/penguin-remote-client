import { describe, expect, it } from "vitest";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import crypto from "node:crypto";
import { prepareRelease, requiredAssets, signingTargets, verifySigningReports } from "../../scripts/prepare-release.mjs";
import { verifyUploadedRelease } from "../../scripts/verify-uploaded-release.mjs";
import { resourcesDirectory } from "../scripts/verify-package.mjs";
import { productionGoUrl, releaseTarget, stagingGoUrl } from "../../scripts/release-target.mjs";
import { updateTargets, stageUpdateMetadata, mergeUpdateMetadata } from "../../scripts/update-metadata.mjs";
import { downloadResourceFiles, generateReleaseDownloadManifest } from "../../scripts/generate-release-download-manifest.mjs";

function writeUpdateFeeds(dir, version) {
  for (const target of updateTargets) {
    fs.writeFileSync(path.join(dir, target.feed), JSON.stringify({ version, files: [{ url: target.asset }] }));
    stageUpdateMetadata(dir, target.platform, target.arch, version);
  }
  mergeUpdateMetadata(dir, version);
  generateReleaseDownloadManifest(dir, "desktop-v" + version);
}

function writeSigningReports(dir, commit = "a".repeat(40), penguinGoUrl = productionGoUrl) {
  for (const target of signingTargets) {
    fs.writeFileSync(path.join(dir, target.report), JSON.stringify({
      platform: target.platform, arch: target.arch, sourceCommit: commit,
      trustedCodeSigning: true, notarized: target.notarized, penguinGoUrl,
      assets: target.assets.map(name => ({
        name, sha256: crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, name))).digest("hex"),
      })),
    }));
  }
}

describe("desktop release gates", () => {
  it("reserves explicit staging versions without changing production defaults", () => {
    expect(releaseTarget("0.1.3")).toEqual({ channel: "production", penguinGoUrl: productionGoUrl, prerelease: false, makeLatest: true });
    expect(releaseTarget("0.1.3-staging.1")).toEqual({ channel: "staging", penguinGoUrl: stagingGoUrl, prerelease: true, makeLatest: false });
    expect(releaseTarget("0.1.4-beta.1")).toEqual({ channel: "production", penguinGoUrl: productionGoUrl, prerelease: true, makeLatest: false });
    for (const version of ["0.1.3-staging", "0.1.3-Staging.1", "0.1.3-staging.01", "0.1.3-staging.1.extra", "invalid"]) {
      expect(() => releaseTarget(version)).toThrow();
    }
  });
  it("requires staging signing reports and records the staging service in the manifest", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-staging-release-test-"));
    try {
      for (const name of requiredAssets) fs.writeFileSync(path.join(dir, name), "staging-signed-artifact");
      writeUpdateFeeds(dir, "0.1.3-staging.1");
      writeSigningReports(dir);
      expect(() => prepareRelease(dir, "desktop-v0.1.3-staging.1", "a".repeat(40), "0.1.3-staging.1")).toThrow();
      writeSigningReports(dir, "a".repeat(40), stagingGoUrl);
      expect(() => verifySigningReports(dir, "a".repeat(40))).toThrow();
      prepareRelease(dir, "desktop-v0.1.3-staging.1", "a".repeat(40), "0.1.3-staging.1");
      const manifest = JSON.parse(fs.readFileSync(path.join(dir, "release-manifest.json"), "utf8"));
      expect(manifest.releaseChannel).toBe("staging");
      expect(manifest.penguinGoUrl).toBe(stagingGoUrl);
    } finally {
      if (!path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep + "remote-staging-release-test-")) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
  it("uses the architecture names emitted by Linux package formats", () => {
    expect(requiredAssets).toContain("penguin-remote-linux-x86_64.AppImage");
    expect(requiredAssets).toContain("penguin-remote-linux-amd64.deb");
    expect(requiredAssets).not.toContain("penguin-remote-linux-x64.deb");
  });
  it("resolves each supported packaged resources directory", () => {
    expect(resourcesDirectory("win","x64")).toBe("win-unpacked/resources");
    expect(resourcesDirectory("linux","x64")).toBe("linux-unpacked/resources");
    expect(resourcesDirectory("mac","x64")).toBe("mac/Penguin Remote.app/Contents/Resources");
    expect(resourcesDirectory("mac","arm64")).toBe("mac-arm64/Penguin Remote.app/Contents/Resources");
    expect(() => resourcesDirectory("win","arm64")).toThrow();
  });
  it("requires all platforms before preparing checksums", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(),"remote-release-test-"));
    try {
      for (const name of requiredAssets) fs.writeFileSync(path.join(dir,name),"test-artifact");
      expect(() => prepareRelease(dir,"desktop-v0.2.0","a".repeat(40),"0.1.0")).toThrow();
      expect(() => prepareRelease(dir,"desktop-v0.1.0","bad","0.1.0")).toThrow();
      fs.unlinkSync(path.join(dir,requiredAssets[0]));
      expect(() => prepareRelease(dir,"desktop-v0.1.0","a".repeat(40),"0.1.0")).toThrow();
      fs.writeFileSync(path.join(dir,requiredAssets[0]),"test-artifact");
      fs.writeFileSync(path.join(dir,"untrusted.txt"),"test");
      expect(() => prepareRelease(dir,"desktop-v0.1.0","a".repeat(40),"0.1.0")).toThrow();
      fs.unlinkSync(path.join(dir,"untrusted.txt"));
      expect(() => prepareRelease(dir,"desktop-v0.1.0","a".repeat(40),"0.1.0")).toThrow();
      writeSigningReports(dir);
      expect(() => prepareRelease(dir,"desktop-v0.1.0","a".repeat(40),"0.1.0")).toThrow();
      writeUpdateFeeds(dir, "0.1.0");
      expect(prepareRelease(dir,"desktop-v0.1.0","a".repeat(40),"0.1.0")).toHaveLength(16);
      const manifest = JSON.parse(fs.readFileSync(path.join(dir,"release-manifest.json"),"utf8"));
      expect(manifest.penguinGoUrl).toBe("https://go.penguin.ooo");
      expect(manifest.trustedCodeSigning).toBe(true);
      expect(manifest.notarized).toBe(true);
      const sums = fs.readFileSync(path.join(dir,"SHA256SUMS.txt"),"utf8");
      expect(sums.trim().split("\n")).toHaveLength(16);
      for (const name of downloadResourceFiles) {
        const hash = crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, name))).digest("hex");
        expect(manifest.assets).toContainEqual({ name, size: fs.statSync(path.join(dir, name)).size, sha256: hash });
        expect(sums).toContain(`${hash}  ${name}\n`);
      }
      const release = {
        draft: true,
        prerelease: true,
        tag_name: manifest.tag,
        target_commitish: manifest.sourceCommit,
        assets: fs.readdirSync(dir).map(name => {
          const bytes = fs.readFileSync(path.join(dir,name));
          return { name, size: bytes.length, digest: "sha256:" + crypto.createHash("sha256").update(bytes).digest("hex") };
        }),
      };
      expect(() => verifyUploadedRelease(dir,release)).not.toThrow();
      expect(() => verifyUploadedRelease(dir,{...release,draft:false})).toThrow();
      expect(() => verifyUploadedRelease(dir,{...release,prerelease:false})).toThrow();
      expect(() => verifyUploadedRelease(dir,{...release,tag_name:"desktop-v0.2.0"})).toThrow();
      expect(() => verifyUploadedRelease(dir,{...release,target_commitish:"b".repeat(40)})).toThrow();
      expect(() => verifyUploadedRelease(dir,{...release,assets:release.assets.slice(1)})).toThrow();
      const badAsset = {...release.assets[0],digest:"sha256:" + "0".repeat(64)};
      expect(() => verifyUploadedRelease(dir,{...release,assets:[badAsset,...release.assets.slice(1)]})).toThrow();
      expect(() => verifyUploadedRelease(dir,{...release,assets:[{...release.assets[0],size:0},...release.assets.slice(1)]})).toThrow();
    } finally {
      if (!path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep + "remote-release-test-")) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir,{recursive:true,force:true});
    }
  });
  it("rejects unsigned, unnotarized, stale and incomplete signing reports", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-signing-test-"));
    try {
      for (const name of requiredAssets) fs.writeFileSync(path.join(dir, name), "signed-artifact");
      writeSigningReports(dir);
      expect(() => verifySigningReports(dir, "a".repeat(40))).not.toThrow();
      expect(() => verifySigningReports(dir, "b".repeat(40))).toThrow();
      for (const target of signingTargets) {
        const file = path.join(dir, target.report);
        const good = JSON.parse(fs.readFileSync(file, "utf8"));
        for (const bad of [
          { ...good, trustedCodeSigning: false },
          { ...good, platform: "linux" },
          { ...good, arch: "unknown" },
          { ...good, penguinGoUrl: stagingGoUrl },
          { ...good, assets: [] },
          { ...good, assets: good.assets.map(asset => ({ ...asset, sha256: "0".repeat(64) })) },
          ...(target.notarized ? [{ ...good, notarized: false }] : []),
          ...(good.assets.length > 1 ? [{ ...good, assets: [good.assets[0], good.assets[0]] }] : []),
        ]) {
          fs.writeFileSync(file, JSON.stringify(bad));
          expect(() => verifySigningReports(dir, "a".repeat(40))).toThrow();
        }
        fs.writeFileSync(file, JSON.stringify(good));
      }
      fs.appendFileSync(path.join(dir, requiredAssets[1]), "changed-after-stapling");
      expect(() => verifySigningReports(dir, "a".repeat(40))).toThrow(/integrity mismatch/);
    } finally {
      if (!path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep + "remote-signing-test-")) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
  it.each(["missing-small", "missing-large", "missing-tsv", "size", "content", "manifest"])("rejects invalid probes before preparation and draft publication: %s", mode => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-probe-release-test-"));
    try {
      for (const name of requiredAssets) fs.writeFileSync(path.join(dir, name), "final-signed-artifact");
      writeSigningReports(dir);
      writeUpdateFeeds(dir, "0.1.7");
      prepareRelease(dir, "desktop-v0.1.7", "a".repeat(40), "0.1.7");
      // Remove preparation output so both gates inspect the same malformed input set.
      const manifestBytes = fs.readFileSync(path.join(dir, "release-manifest.json"));
      const sumBytes = fs.readFileSync(path.join(dir, "SHA256SUMS.txt"));
      fs.unlinkSync(path.join(dir, "release-manifest.json"));
      fs.unlinkSync(path.join(dir, "SHA256SUMS.txt"));
      const missing = { "missing-small": downloadResourceFiles[0], "missing-large": downloadResourceFiles[1], "missing-tsv": downloadResourceFiles[2] }[mode];
      if (missing) fs.unlinkSync(path.join(dir, missing));
      if (mode === "size") fs.truncateSync(path.join(dir, "probe-64k.bin"), 65535);
      if (mode === "content") {
        const file = path.join(dir, "probe-1m.bin"), bytes = fs.readFileSync(file);
        bytes[0] ^= 1;
        fs.writeFileSync(file, bytes);
      }
      if (mode === "manifest") {
        const file = path.join(dir, "release-download-manifest.tsv");
        fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("desktop-v0.1.7", "desktop-v9.0.0"));
      }
      expect(() => prepareRelease(dir, "desktop-v0.1.7", "a".repeat(40), "0.1.7")).toThrow();
      fs.writeFileSync(path.join(dir, "release-manifest.json"), manifestBytes);
      fs.writeFileSync(path.join(dir, "SHA256SUMS.txt"), sumBytes);
      const release = { draft: true, prerelease: true, tag_name: "desktop-v0.1.7", target_commitish: "a".repeat(40),
        assets: fs.readdirSync(dir).map(name => {
          const bytes = fs.readFileSync(path.join(dir, name));
          return { name, size: bytes.length, digest: "sha256:" + crypto.createHash("sha256").update(bytes).digest("hex") };
        }) };
      expect(() => verifyUploadedRelease(dir, release)).toThrow();
    } finally {
      if (!path.resolve(dir).startsWith(path.join(os.tmpdir(), "remote-probe-release-test-"))) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
  it.each(downloadResourceFiles)("requires %s in both publication manifests even when GitHub digests match", name => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-probe-records-test-"));
    try {
      for (const file of requiredAssets) fs.writeFileSync(path.join(dir, file), "final-signed-artifact");
      writeSigningReports(dir);
      writeUpdateFeeds(dir, "0.1.7");
      prepareRelease(dir, "desktop-v0.1.7", "a".repeat(40), "0.1.7");
      const jsonFile = path.join(dir, "release-manifest.json"), sumFile = path.join(dir, "SHA256SUMS.txt");
      const originalManifest = fs.readFileSync(jsonFile, "utf8"), originalSums = fs.readFileSync(sumFile, "utf8");
      for (const mode of ["json", "sums"]) {
        const manifest = JSON.parse(originalManifest);
        if (mode === "json") manifest.assets = manifest.assets.filter(asset => asset.name !== name);
        fs.writeFileSync(jsonFile, JSON.stringify(manifest));
        fs.writeFileSync(sumFile, mode === "sums" ? originalSums.split("\n").filter(line => !line.endsWith("  " + name)).join("\n") : originalSums);
        const release = { draft: true, prerelease: true, tag_name: manifest.tag, target_commitish: manifest.sourceCommit,
          assets: fs.readdirSync(dir).map(file => {
            const bytes = fs.readFileSync(path.join(dir, file));
            return { name: file, size: bytes.length, digest: "sha256:" + crypto.createHash("sha256").update(bytes).digest("hex") };
          }) };
        expect(() => verifyUploadedRelease(dir, release)).toThrow(/Download resource manifest\/checksum mismatch/);
      }
    } finally {
      if (!path.resolve(dir).startsWith(path.join(os.tmpdir(), "remote-probe-records-test-"))) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
});
