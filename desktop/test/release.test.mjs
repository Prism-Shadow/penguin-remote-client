import { describe, expect, it } from "vitest";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import crypto from "node:crypto";
import { prepareRelease, requiredAssets, signingTargets, verifySigningReports } from "../../scripts/prepare-release.mjs";
import { verifyUploadedRelease } from "../../scripts/verify-uploaded-release.mjs";
import { resourcesDirectory } from "../scripts/verify-package.mjs";

function writeSigningReports(dir, commit = "a".repeat(40)) {
  for (const target of signingTargets) {
    fs.writeFileSync(path.join(dir, target.report), JSON.stringify({
      platform: target.platform, arch: target.arch, sourceCommit: commit,
      trustedCodeSigning: true, notarized: target.notarized,
      assets: target.assets.map(name => ({
        name, sha256: crypto.createHash("sha256").update(fs.readFileSync(path.join(dir, name))).digest("hex"),
      })),
    }));
  }
}

describe("desktop release gates", () => {
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
      expect(prepareRelease(dir,"desktop-v0.1.0","a".repeat(40),"0.1.0")).toHaveLength(10);
      const manifest = JSON.parse(fs.readFileSync(path.join(dir,"release-manifest.json"),"utf8"));
      expect(manifest.penguinGoUrl).toBe("https://go.penguin.ooo");
      expect(manifest.trustedCodeSigning).toBe(true);
      expect(manifest.notarized).toBe(true);
      expect(fs.readFileSync(path.join(dir,"SHA256SUMS.txt"),"utf8").trim().split("\n")).toHaveLength(10);
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
});
