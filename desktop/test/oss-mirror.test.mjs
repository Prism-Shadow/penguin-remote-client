import { afterEach, describe, expect, it, vi } from "vitest";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import yaml from "yaml";
import { requiredAssets, signingTargets, prepareRelease } from "../../scripts/prepare-release.mjs";
import { releaseTarget } from "../../scripts/release-target.mjs";
import { stageUpdateMetadata, mergeUpdateMetadata, updateTargets } from "../../scripts/update-metadata.mjs";
import { sha256, verifyReleaseMirror, verifyPublicRelease, releaseVersion } from "../../scripts/verify-release-mirror.mjs";
import { downloadReleaseMirror } from "../../scripts/download-release-mirror.mjs";
import { mirrorRelease } from "../../scripts/publish-release-to-oss.mjs";
import { ossClient, ossConfig, githubClient } from "../../scripts/oss-release-tools.mjs";
import { testOssStaging } from "../../scripts/test-oss-staging.mjs";
import { downloadResourceFiles, generateReleaseDownloadManifest } from "../../scripts/generate-release-download-manifest.mjs";

const directories = [];
const config = { publicBase: "https://client-releases.example.test", bucket: "client-releases", region: "cn-beijing", endpoint: "oss-cn-beijing.aliyuncs.com" };
const commit = "a".repeat(40);
function directory() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-oss-test-"));
  directories.push(dir);
  return dir;
}
afterEach(() => {
  for (const dir of directories.splice(0)) {
    if (!path.resolve(dir).startsWith(path.join(os.tmpdir(), "remote-oss-test-"))) throw new Error("Unsafe cleanup path");
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

function assetMetadata(dir) {
  return fs.readdirSync(dir).map(name => {
    const bytes = fs.readFileSync(path.join(dir, name));
    return { name, size: bytes.length, digest: "sha256:" + sha256(bytes) };
  });
}
const historicalSources = {
  "0.1.0": "47ae323454abb7558f980676237879a03c104478",
  "0.1.1": "d120c81641c76cc40a240f92980e6ce6ee5b61c3",
  "0.1.2": "9130258e82acd219c5d1ee2ad2e0334434fc53e1",
  "0.1.3-staging.1": "e5b30be662bc3df1297976907067d52e1b2db8d2",
  "0.1.3": "de6b84e0a0d87bbf5a2c7dafcfd2013dbdccce79",
  "0.1.4": "8897e13300f87effc00c9fab4e837a33313fce79",
  "0.1.5": "98edd11798b054f3cde5f131a25149289cd538d6",
  "0.1.6": "147d71a22aea87e2b1abb0807e48c73c4d4f4f45",
};
function fixture({ version = "0.1.7", historical = Object.hasOwn(historicalSources, version),
  unsigned = ["0.1.0", "0.1.1", "0.1.2"].includes(version), legacyLinux = version === "0.1.0",
  sourceCommit = historicalSources[version] ?? commit } = {}) {
  const dir = directory(), target = releaseTarget(version), tag = "desktop-v" + version;
  const installers = requiredAssets.map(name => legacyLinux ? name.replace("linux-x86_64", "linux-x64").replace("linux-amd64", "linux-x64") : name);
  for (const name of installers) fs.writeFileSync(path.join(dir, name), "final-artifact-" + name);
  for (const name of [installers[0], installers[2], installers[4]]) fs.writeFileSync(path.join(dir, name + ".blockmap"), "blockmap-" + name);
  if (!unsigned) for (const signing of signingTargets) {
    fs.writeFileSync(path.join(dir, signing.report), JSON.stringify({ ...signing, sourceCommit,
      trustedCodeSigning: true, penguinGoUrl: target.penguinGoUrl,
      assets: signing.assets.map(name => ({ name, sha256: sha256(fs.readFileSync(path.join(dir, name))) })) }));
  }
  if (!historical || (version === "0.1.6" && !legacyLinux)) {
    for (const update of updateTargets) {
      fs.writeFileSync(path.join(dir, update.feed), JSON.stringify({ version, files: [{ url: update.asset }] }));
      stageUpdateMetadata(dir, update.platform, update.arch, version);
    }
    mergeUpdateMetadata(dir, version);
  }
  if (!historical) {
    generateReleaseDownloadManifest(dir, tag);
    prepareRelease(dir, tag, sourceCommit, version);
  } else {
    const assets = assetMetadata(dir).map(asset => ({ name: asset.name, size: asset.size, sha256: asset.digest.slice(7) }));
    fs.writeFileSync(path.join(dir, "release-manifest.json"), JSON.stringify({ version, tag, sourceCommit,
      penguinGoUrl: target.penguinGoUrl, trustedCodeSigning: !unsigned, notarized: !unsigned, assets }));
    fs.writeFileSync(path.join(dir, "SHA256SUMS.txt"), assets.map(asset => `${asset.sha256}  ${asset.name}`).join("\n") + "\n");
  }
  const release = { id: 123, tag_name: tag, target_commitish: sourceCommit, draft: false, prerelease: target.prerelease,
    published_at: "2026-10-22T00:00:00Z", assets: assetMetadata(dir) };
  const github = { release: vi.fn(() => structuredClone(release)), latest: vi.fn(() => structuredClone(release)) };
  return { dir, directory: dir, tag, release, github, config, log: vi.fn() };
}
function fakeOss() {
  const objects = new Map();
  return {
    objects,
    get: vi.fn(key => objects.get(key) ?? null),
    put: vi.fn((key, bytes, options) => {
      if (options?.immutable && objects.has(key)) throw new Error("FileAlreadyExists: HTTP 409");
      objects.set(key, Buffer.from(bytes));
    }),
  };
}
function editManifest(f, edit) {
  const file = path.join(f.dir, "release-manifest.json"), manifest = JSON.parse(fs.readFileSync(file, "utf8"));
  edit(manifest);
  fs.writeFileSync(file, JSON.stringify(manifest));
  f.release.assets = assetMetadata(f.dir);
}
function removeContractAssets(f, names, edit = () => {}) {
  for (const name of names) fs.unlinkSync(path.join(f.dir, name));
  editManifest(f, manifest => {
    manifest.assets = manifest.assets.filter(asset => !names.includes(asset.name));
    edit(manifest);
  });
  const manifest = JSON.parse(fs.readFileSync(path.join(f.dir, "release-manifest.json"), "utf8"));
  fs.writeFileSync(path.join(f.dir, "SHA256SUMS.txt"), manifest.assets.map(asset => `${asset.sha256}  ${asset.name}`).join("\n") + "\n");
  f.release.assets = assetMetadata(f.dir);
}

describe("published Client asset verification", () => {
  it("verifies all current installers, reports, feeds and blockmaps including metadata digests", () => {
    const f = fixture(), result = verifyReleaseMirror(f.dir, f.release, f.tag);
    expect(result.files).toEqual(fs.readdirSync(f.dir).sort());
    expect(result.files).toContain("latest-mac.yml");
    expect(result.files).toContain("penguin-remote-win-x64.exe.blockmap");
    expect(result.files).toContain("release-manifest.json");
    expect(result.files).toContain("SHA256SUMS.txt");
  });
  it.each(Object.keys(historicalSources))("preserves the audited historical contract for %s without adding probes", version => {
    const f = fixture({ version });
    expect(verifyReleaseMirror(f.dir, f.release, f.tag).manifest.version).toBe(version);
    expect(fs.existsSync(path.join(f.dir, "latest.yml"))).toBe(version === "0.1.6");
    for (const name of downloadResourceFiles) expect(fs.existsSync(path.join(f.dir, name))).toBe(false);
  });
  it.each(Object.keys(historicalSources))("rejects a changed source for historical tag %s even when all source fields and digests agree", version => {
    const f = fixture({ version, sourceCommit: "b".repeat(40) });
    expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow(/Historical release sourceCommit mismatch/);
  });
  it("does not extend historical exceptions to unknown prerelease tags or later versions", () => {
    for (const version of ["0.1.2-beta.1", "0.1.3-staging.2", "0.1.6-rc.1", "0.2.0"]) {
      const f = fixture({ version, historical: true, unsigned: true });
      expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow(/signing contract mismatch/);
    }
  });
  it.each(["0.1.1", "0.1.6"])("does not accept the legacy Linux filename contract for %s", version => {
    const f = fixture({ version, historical: true, legacyLinux: true });
    expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow(/Missing required Client release asset/);
  });
  it.each(["v0.1.6", "desktop-v0.1.6/evil", "desktop-v0.1.6-..", "desktop-v0.1.6-staging.01", "-bad"])("rejects unsafe/non-Client tag %s", tag => {
    expect(() => releaseVersion(tag)).toThrow();
  });
  it.each([
    { draft: true }, { published_at: null }, { tag_name: "desktop-v9.0.0" }, { assets: [] },
    { prerelease: undefined }, { id: undefined },
  ])("rejects non-public or mismatched releases: %j", changes => {
    const f = fixture();
    expect(() => verifyPublicRelease({ ...f.release, ...changes }, f.tag)).toThrow();
  });
  it.each(["missing", "extra", "directory", "changed", "duplicate", "size", "digest", "unsafe"])("rejects GitHub/local asset mismatch: %s", mode => {
    const f = fixture(), name = requiredAssets[0];
    if (mode === "missing") fs.unlinkSync(path.join(f.dir, name));
    if (mode === "extra") fs.writeFileSync(path.join(f.dir, "extra.txt"), "extra");
    if (mode === "directory") fs.mkdirSync(path.join(f.dir, "nested"));
    if (mode === "changed") fs.appendFileSync(path.join(f.dir, name), "changed");
    if (mode === "duplicate") f.release.assets[1] = f.release.assets[0];
    if (mode === "size") f.release.assets[0].size++;
    if (mode === "digest") f.release.assets[0].digest = null;
    if (mode === "unsafe") f.release.assets[0].name = "../evil";
    expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow();
  });
  it.each(["release-manifest.json", "SHA256SUMS.txt"])("cross-checks %s itself with GitHub metadata", name => {
    const f = fixture();
    fs.appendFileSync(path.join(f.dir, name), " ");
    expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow(/GitHub asset integrity/);
  });
  it.each([
    manifest => { manifest.tag = "desktop-v9.0.0"; },
    manifest => { manifest.version = "9.0.0"; },
    manifest => { manifest.sourceCommit = "b".repeat(40); },
    manifest => { manifest.sourceCommit = "main"; },
    manifest => { manifest.penguinGoUrl = "https://wrong.example.test"; },
    manifest => { manifest.releaseChannel = "staging"; },
    manifest => { manifest.assets.pop(); },
    manifest => { manifest.assets[0].sha256 = "0".repeat(64); },
    manifest => { manifest.assets[0].size++; },
    manifest => { manifest.assets[1] = manifest.assets[0]; },
    manifest => { manifest.trustedCodeSigning = false; },
  ])("rejects invalid manifest contract even when GitHub hashes match", edit => {
    const f = fixture();
    editManifest(f, edit);
    expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow();
  });
  it("rejects invalid checksums even with an updated GitHub digest", () => {
    const f = fixture();
    fs.writeFileSync(path.join(f.dir, "SHA256SUMS.txt"), "0".repeat(64) + "  " + requiredAssets[0] + "\n");
    f.release.assets = assetMetadata(f.dir);
    expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow(/SHA256SUMS/);
  });
  it("rejects missing declared feeds and signing reports", () => {
    for (const name of ["latest.yml", "signing-windows-x64.json"]) {
      const f = fixture();
      fs.unlinkSync(path.join(f.dir, name));
      f.release.assets = assetMetadata(f.dir);
      expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow();
    }
  });
  it("rejects missing installers or partial feeds even if all three metadata layers agree", () => {
    for (const name of [requiredAssets[0], "latest.yml"]) {
      const f = fixture();
      removeContractAssets(f, [name]);
      expect(() => verifyReleaseMirror(f.dir, f.release, f.tag)).toThrow();
    }
  });
  it.each(["0.1.6", "0.1.6-staging.1", "0.2.0"])("rejects all feeds removed from %s even when every metadata layer agrees, before OSS access", version => {
    const f = fixture({ version }), oss = fakeOss();
    removeContractAssets(f, ["latest.yml", "latest-mac.yml", "latest-linux.yml"]);
    expect(() => mirrorRelease({ ...f, oss })).toThrow();
    expect(oss.get).not.toHaveBeenCalled();
    expect(oss.put).not.toHaveBeenCalled();
  });
  it.each(["0.1.3", "0.1.6", "0.1.6-staging.1", "0.2.0"])("rejects signing contract downgrade for %s even when every metadata layer agrees, before OSS access", version => {
    const f = fixture({ version }), oss = fakeOss();
    removeContractAssets(f, signingTargets.map(target => target.report), manifest => {
      manifest.trustedCodeSigning = false;
      manifest.notarized = false;
    });
    expect(() => mirrorRelease({ ...f, oss })).toThrow();
    expect(oss.get).not.toHaveBeenCalled();
    expect(oss.put).not.toHaveBeenCalled();
  });
  it("refuses draft/not-found downloads before creating a destination or invoking download", () => {
    const f = fixture(), dest = path.join(f.dir, "download"), download = vi.fn();
    expect(() => downloadReleaseMirror(dest, f.tag, { release: () => ({ ...f.release, draft: true }), download })).toThrow();
    expect(() => downloadReleaseMirror(dest, f.tag, { release: () => { throw new Error("HTTP 404"); }, download })).toThrow();
    expect(fs.existsSync(dest)).toBe(false);
    expect(download).not.toHaveBeenCalled();
  });
  it("downloads into an empty destination and verifies exact original bytes", () => {
    const f = fixture(), dest = path.join(directory(), "download");
    const download = vi.fn((tag, target) => {
      expect(tag).toBe(f.tag);
      for (const name of fs.readdirSync(f.dir)) fs.copyFileSync(path.join(f.dir, name), path.join(target, name));
    });
    expect(downloadReleaseMirror(dest, f.tag, { release: () => f.release, download }).files).toHaveLength(f.release.assets.length);
    expect(() => downloadReleaseMirror(dest, f.tag, { release: () => f.release, download })).toThrow();
    expect(download).toHaveBeenCalledTimes(1);
  });
});

describe("immutable OSS mirrors and latest pointer", () => {
  it("uploads all exact bytes once and reruns idempotently with immutable caching", () => {
    const f = fixture(), oss = fakeOss();
    expect(mirrorRelease({ ...f, oss }).updatedLatest).toBe(true);
    const calls = oss.put.mock.calls.filter(([key]) => key !== "latest.json");
    expect(calls).toHaveLength(f.release.assets.length);
    for (const [key, bytes, options] of calls) {
      expect(bytes.equals(fs.readFileSync(path.join(f.dir, key.split("/").at(-1))))).toBe(true);
      expect(options.immutable).toBe(true);
      expect(options.cache).toContain("public,max-age=31536000,immutable");
      if (key.endsWith(".bin")) {
        expect(options.cache).toContain("no-transform");
        expect(options.contentType).toBe("application/octet-stream");
      }
    }
    expect(JSON.parse(oss.objects.get("latest.json"))).toEqual({ schemaVersion: 1, tag: f.tag, version: "0.1.7", releaseBaseUrl: `${config.publicBase}/releases/${f.tag}` });
    for (const name of downloadResourceFiles) {
      expect(oss.objects.get(`releases/${f.tag}/${name}`).equals(fs.readFileSync(path.join(f.dir, name)))).toBe(true);
    }
    expect(oss.put.mock.calls.at(-1)[2]).toEqual({ cache: "no-cache" });
    mirrorRelease({ ...f, oss });
    expect(oss.put.mock.calls.filter(([key]) => key !== "latest.json")).toHaveLength(calls.length);
  });
  it("does not upload anything when validation fails", () => {
    const f = fixture(), oss = fakeOss();
    fs.unlinkSync(path.join(f.dir, requiredAssets[0]));
    expect(() => mirrorRelease({ ...f, oss })).toThrow();
    expect(oss.get).not.toHaveBeenCalled();
    expect(oss.put).not.toHaveBeenCalled();
  });
  it("fails conflicting objects without overwriting them or updating latest", () => {
    const f = fixture(), oss = fakeOss(), key = `releases/${f.tag}/SHA256SUMS.txt`;
    oss.objects.set(key, Buffer.from("conflict"));
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/content mismatch/);
    expect(oss.put).not.toHaveBeenCalled();
    expect(oss.objects.get(key).toString()).toBe("conflict");
  });
  it.each([false, true])("verifies a concurrent create winner; different bytes fail: %s", conflict => {
    const f = fixture(), oss = fakeOss();
    oss.put.mockImplementation((key, bytes) => {
      oss.objects.set(key, Buffer.from(conflict ? "conflict" : bytes));
      throw new Error("FileAlreadyExists");
    });
    f.github.latest.mockReturnValue(null);
    if (conflict) expect(() => mirrorRelease({ ...f, oss })).toThrow(/content mismatch/);
    else expect(mirrorRelease({ ...f, oss }).updatedLatest).toBe(false);
  });
  it("fails when uploaded bytes differ on readback", () => {
    const f = fixture(), oss = fakeOss();
    oss.put.mockImplementation(key => oss.objects.set(key, Buffer.from("corrupted in transport")));
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/content mismatch/);
    expect(oss.objects.has("latest.json")).toBe(false);
  });
  it("fails when a failed upload leaves no object", () => {
    const f = fixture(), oss = fakeOss();
    oss.put.mockImplementation(() => { throw new Error("network unavailable"); });
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/content mismatch/);
  });
  it.each(["0.1.3", "0.1.6-beta.1", "0.1.6-staging.1"])("mirrors %s without replacing Latest", version => {
    const f = fixture({ version }), oss = fakeOss();
    if (version === "0.1.3") f.github.latest.mockReturnValue({ ...f.release, tag_name: "desktop-v0.1.7" });
    oss.objects.set("latest.json", Buffer.from("existing pointer"));
    expect(mirrorRelease({ ...f, oss }).updatedLatest).toBe(false);
    expect(oss.objects.get("latest.json").toString()).toBe("existing pointer");
    expect(oss.put.mock.calls.some(([key]) => key === "latest.json")).toBe(false);
    expect(oss.objects.size).toBe(f.release.assets.length + 1);
  });
  it("also excludes stable-tag releases explicitly marked prerelease", () => {
    const f = fixture(), oss = fakeOss();
    f.release.prerelease = true;
    expect(mirrorRelease({ ...f, oss }).updatedLatest).toBe(false);
    expect(f.github.latest).not.toHaveBeenCalled();
  });
  it("rechecks GitHub Latest after uploads, preventing an older mirror from rolling back the pointer", () => {
    const f = fixture(), oss = fakeOss();
    f.github.latest.mockReturnValueOnce(f.release).mockReturnValue({ ...f.release, tag_name: "desktop-v0.1.8", id: 124 });
    expect(mirrorRelease({ ...f, oss }).updatedLatest).toBe(false);
    expect(f.github.latest).toHaveBeenCalledTimes(2);
    expect(oss.objects.has("latest.json")).toBe(false);
  });
  it("keeps latest unchanged if a release becomes draft/prerelease or is recreated during transfer", () => {
    for (const changes of [{ draft: true }, { prerelease: true }, { id: 124 }]) {
      const f = fixture(), oss = fakeOss();
      f.github.release.mockReturnValueOnce(f.release).mockReturnValue({ ...f.release, ...changes });
      expect(mirrorRelease({ ...f, oss }).updatedLatest).toBe(false);
      expect(oss.objects.has("latest.json")).toBe(false);
    }
  });
  it("verifies latest.json itself after writing", () => {
    const f = fixture(), oss = fakeOss(), original = oss.put.getMockImplementation();
    oss.put.mockImplementation((key, bytes, options) => original(key, key === "latest.json" ? Buffer.from("wrong pointer") : bytes, options));
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/content mismatch: latest.json/);
  });
  it.each([...downloadResourceFiles, "all"])("requires the complete new probe contract before any OSS mutation: %s", name => {
    const f = fixture(), oss = fakeOss();
    removeContractAssets(f, name === "all" ? downloadResourceFiles : [name]);
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/Missing required Client release asset/);
    expect(oss.get).not.toHaveBeenCalled();
    expect(oss.put).not.toHaveBeenCalled();
  });
  it.each(["size", "content", "tsv-tag", "tsv-hash"])("rejects probes even when GitHub and JSON/checksum metadata agree: %s", mode => {
    const f = fixture(), oss = fakeOss(), file = path.join(f.dir, mode.startsWith("tsv") ? "release-download-manifest.tsv" : "probe-64k.bin");
    if (mode === "size") fs.truncateSync(file, 65535);
    if (mode === "content") {
      const bytes = fs.readFileSync(file); bytes[0] ^= 1; fs.writeFileSync(file, bytes);
    }
    if (mode === "tsv-tag") fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace(f.tag, "desktop-v9.0.0"));
    if (mode === "tsv-hash") fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace(/[a-f0-9]{64}/, "0".repeat(64)));
    editManifest(f, manifest => {
      for (const asset of manifest.assets) {
        const bytes = fs.readFileSync(path.join(f.dir, asset.name));
        asset.size = bytes.length; asset.sha256 = sha256(bytes);
      }
      fs.writeFileSync(path.join(f.dir, "SHA256SUMS.txt"), manifest.assets.map(asset => `${asset.sha256}  ${asset.name}`).join("\n") + "\n");
    });
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/download (probe|manifest)/);
    expect(oss.put).not.toHaveBeenCalled();
  });
  it.each(downloadResourceFiles)("does not update Latest if %s fails readback after a partial mirror", name => {
    const f = fixture(), oss = fakeOss(), put = oss.put.getMockImplementation();
    oss.put.mockImplementation((key, bytes, options) => put(key, key.endsWith("/" + name) ? Buffer.from("corrupt") : bytes, options));
    expect(() => mirrorRelease({ ...f, oss })).toThrow(/content mismatch/);
    expect(oss.put.mock.calls.some(([key]) => key === "latest.json")).toBe(false);
    expect(oss.objects.has("latest.json")).toBe(false);
  });
});

describe("exact-key ossutil and GitHub adapters", () => {
  it("requires Environment addresses and accepts Beijing or acceleration endpoints", () => {
    const env = { OSS_BUCKET: config.bucket, OSS_REGION: config.region, OSS_ENDPOINT: config.endpoint, OSS_PUBLIC_BASE_URL: config.publicBase };
    expect(ossConfig(env)).toEqual(config);
    expect(ossConfig({ ...env, OSS_ENDPOINT: "oss-accelerate.aliyuncs.com" }).endpoint).toContain("accelerate");
    for (const key of Object.keys(env)) expect(() => ossConfig({ ...env, [key]: "" })).toThrow();
    expect(() => ossConfig({ ...env, OSS_PUBLIC_BASE_URL: "http://unsafe.test" })).toThrow();
    expect(() => ossConfig({ ...env, OSS_PUBLIC_BASE_URL: "https://example.test/prefix" })).toThrow();
  });
  it("uses forbid-overwrite, cache headers and exact-key get without List/Delete", () => {
    const scratch = directory(), calls = [];
    const run = (binary, args) => {
      calls.push({ binary, args });
      if (args[0] === "cp") fs.writeFileSync(args[2], "content");
      else expect(fs.readFileSync(args[args.indexOf("--body") + 1].slice(7), "utf8")).toBe("content");
    };
    const oss = ossClient(config, scratch, run, "fake-ossutil");
    oss.put("releases/desktop-v0.1.6/a.zip", Buffer.from("content"), { immutable: true, cache: "immutable" });
    expect(oss.get("releases/desktop-v0.1.6/a.zip").toString()).toBe("content");
    expect(calls[0].args).toContain("--forbid-overwrite");
    expect(calls[0].args).toContain("--cache-control");
    expect(calls[1].args[1]).toBe("oss://client-releases/releases/desktop-v0.1.6/a.zip");
    oss.put("releases/desktop-v0.1.7/probe-64k.bin", Buffer.from("content"), {
      immutable: true, cache: "public,max-age=31536000,immutable,no-transform", contentType: "application/octet-stream",
    });
    expect(calls[2].args).toContain("--content-type");
    expect(calls[2].args).toContain("application/octet-stream");
    expect(calls[2].args).not.toContain("--content-encoding");
    expect(fs.readdirSync(scratch)).toEqual([]);
  });
  it("only treats NoSuchKey as absent; access-denied and network errors fail closed", () => {
    for (const message of ["NoSuchKey", "AccessDenied HTTP 403", "network timeout"]) {
      const oss = ossClient(config, directory(), () => { throw new Error(message); });
      if (message === "NoSuchKey") expect(oss.get("key")).toBe(null);
      else expect(() => oss.get("key")).toThrow(message);
    }
  });
  it("uses only read-only GitHub operations; only a Latest 404 means no stable release", () => {
    const calls = [], run = (binary, args) => { calls.push([binary, args]); return "{}"; };
    const gh = githubClient("owner/repo", run);
    gh.release("desktop-v0.1.6"); gh.latest(); gh.download("desktop-v0.1.6", "assets");
    expect(calls.map(([, args]) => args[0])).toEqual(["api", "api", "release"]);
    expect(calls[2][1]).toEqual(["release", "download", "desktop-v0.1.6", "--repo", "owner/repo", "--dir", "assets"]);
    expect(githubClient("owner/repo", () => { throw new Error("HTTP 404"); }).latest()).toBe(null);
    expect(() => githubClient("owner/repo", () => { throw new Error("HTTP 403"); }).latest()).toThrow();
  });
});

describe("staging permission probes without real credentials or network", () => {
  function staging() {
    const oss = fakeOss(), original = oss.put.getMockImplementation();
    oss.put.mockImplementation((key, bytes, options) => {
      if (!key.startsWith("staging/")) throw new Error("AccessDenied HTTP 403");
      original(key, bytes, options);
    });
    return { oss, config, log: vi.fn(), run: "offline-test" };
  }
  it("works on a new Bucket and reports latest denial as pending without any pointer write", async () => {
    const f = staging(), fetchPublic = vi.fn(async () => ({ status: 404, ok: false }));
    expect(await testOssStaging({ ...f, fetchPublic })).toEqual({ staging: "passed", releasesProbe: "passed", latestProbe: "pending" });
    expect(f.log.mock.calls.flat().join("\n")).toContain("NOT EXECUTED");
    expect(f.oss.put.mock.calls.some(([key]) => key === "latest.json")).toBe(false);
    expect(f.oss.objects.size).toBe(1);
  });
  it("probes existing latest with exactly its public bytes and forbid-overwrite", async () => {
    const f = staging(), bytes = Buffer.from('{"schemaVersion":1}');
    const result = await testOssStaging({ ...f, fetchPublic: async () => ({ status: 200, ok: true, arrayBuffer: async () => bytes }) });
    expect(result.latestProbe).toBe("passed");
    expect(f.oss.put.mock.calls.at(-1)).toEqual(["latest.json", bytes, { immutable: true }]);
  });
  it("fails unexpected production authorization, including FileAlreadyExists", async () => {
    for (const message of [null, "FileAlreadyExists HTTP 409", "network timeout"]) {
      const f = staging(), original = f.oss.put.getMockImplementation();
      f.oss.put.mockImplementation((key, bytes, options) => {
        if (key === "latest.json") { if (message) throw new Error(message); return; }
        return original(key, bytes, options);
      });
      await expect(testOssStaging({ ...f, fetchPublic: async () => ({ status: 200, ok: true, arrayBuffer: async () => Buffer.from("existing") }) })).rejects.toThrow();
    }
    const f = staging();
    await expect(testOssStaging({ ...f, oss: fakeOss(), fetchPublic: vi.fn() })).rejects.toThrow(/unexpectedly wrote/);
  });
  it("does not misreport non-permission errors or public GET failures as permission success", async () => {
    for (const status of [403, 500]) {
      const f = staging();
      await expect(testOssStaging({ ...f, fetchPublic: async () => ({ status, ok: false }) })).rejects.toThrow(/GET failed/);
    }
    const f = staging(), original = f.oss.put.getMockImplementation();
    f.oss.put.mockImplementation((key, bytes, options) => {
      if (key.startsWith("releases/")) throw new Error("network timeout");
      return original(key, bytes, options);
    });
    await expect(testOssStaging({ ...f, fetchPublic: vi.fn() })).rejects.toThrow(/without AccessDenied/);
  });
  it("checks staging round-trip contents", async () => {
    const f = staging();
    f.oss.get.mockReturnValue(Buffer.from("bad bytes"));
    await expect(testOssStaging({ ...f, fetchPublic: vi.fn() })).rejects.toThrow(/content mismatch/);
  });
});

describe("workflow publishing boundaries", () => {
  const root = path.resolve(import.meta.dirname, "../..");
  const read = file => fs.readFileSync(path.join(root, file), "utf8");
  const workflow = name => yaml.parse(read(`.github/workflows/${name}.yml`));
  it("only mirrors after successful tag publication, retaining rehearsal and signing gates", () => {
    const build = workflow("desktop-build"), job = build.jobs["mirror-oss"];
    expect(job.needs).toBe("release");
    expect(job.if).toBe("github.event_name == 'push' && startsWith(github.ref, 'refs/tags/desktop-v') && needs.release.result == 'success'");
    expect(job.uses).toBe("./.github/workflows/oss-mirror.yml");
    expect(build.jobs.release.permissions).toEqual({ contents: "write" });
    expect(build.jobs.release.steps.at(-1).run).toContain("node scripts/verify-uploaded-release.mjs");
    expect(build.jobs.release.steps.at(-1).run).toContain("gh release edit");
    expect(build.jobs.package.env.PENGUIN_REMOTE_GO_URL).toContain("needs.check.outputs.penguin_go_url");
    const steps = build.jobs.release.steps;
    const generation = steps.findIndex(step => step.name === "Generate release download probes and manifest");
    expect(generation).toBeGreaterThan(steps.findIndex(step => step.name === "Merge update feeds for all architectures"));
    expect(generation).toBeLessThan(steps.findIndex(step => step.name === "Prepare complete release"));
    expect(steps[generation].run).toContain('generate-release-download-manifest.mjs "$RELEASE_TAG" release-assets');
    expect(steps.at(-1).run).toContain('gh release upload "$RELEASE_TAG" release-assets/*');
  });
  it("serializes every mirror entry, scopes OIDC and pins authentication and ossutil", () => {
    const mirror = workflow("oss-mirror"), staging = workflow("oss-staging");
    expect(Object.keys(mirror.on).sort()).toEqual(["workflow_call", "workflow_dispatch"]);
    expect(mirror.on.workflow_dispatch.inputs.tag.required).toBe(true);
    expect(Object.keys(staging.on)).toEqual(["workflow_dispatch"]);
    expect(mirror.jobs.mirror.concurrency).toEqual({ group: "penguin-remote-client-oss-production", "cancel-in-progress": false });
    for (const [workflow, name, environment] of [[mirror, "mirror", "oss-production"], [staging, "staging", "oss-staging"]]) {
      const job = workflow.jobs[name];
      expect(workflow.permissions).toEqual({});
      expect(job.permissions).toEqual({ contents: "read", "id-token": "write" });
      expect(job.environment.name).toBe(environment);
      for (const key of ["ALIYUN_OIDC_PROVIDER_ARN", "ALIYUN_ROLE_ARN", "OSS_BUCKET", "OSS_REGION", "OSS_ENDPOINT", "OSS_PUBLIC_BASE_URL"]) {
        expect(job.env[key]).toBe("${{ vars." + key + " }}");
      }
      const auth = job.steps.find(step => step.id === "aliyun");
      expect(auth.uses).toBe("aliyun/configure-aliyun-credentials-action@1e5248c8d5d93a8781ac344a68e19a43341e79e6");
      expect(auth.with.audience).toBe("github-actions");
    }
    const install = read("scripts/install-ossutil.sh");
    expect(install).toContain('VERSION="2.3.0"');
    expect(install).toContain('ARCHIVE_SHA256="3ae4d9fc85a7a6e9f5654d1599766f1a3a42a3692870887b5ae9338d582ef65a"');
    expect(install).toContain("sha256sum -c -");
    const steps = mirror.jobs.mirror.steps;
    expect(steps.findIndex(step => step.name === "Download and verify published GitHub assets")).toBeLessThan(steps.findIndex(step => step.id === "aliyun"));
  });
});
