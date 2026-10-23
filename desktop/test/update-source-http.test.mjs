import http from "node:http";
import crypto from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ElectronHttpExecutor } from "electron-updater/out/electronHttpExecutor.js";
import { GenericProvider } from "electron-updater/out/providers/GenericProvider.js";
import { findFile } from "electron-updater/out/providers/Provider.js";
import electronUpdater from "electron-updater";
import { WindowsUpdater } from "../src/windows-updater.js";
import { UpdateController } from "../src/update-controller.js";
import {
  GITHUB_LATEST_URL, GITHUB_RELEASE_ROOT, OSS_LATEST_URL, OSS_RELEASE_ROOT, selectUpdateFeed,
} from "../src/update-source.js";
import { generateReleaseDownloadManifest, makeProbe } from "../../scripts/generate-release-download-manifest.mjs";

const tag = "desktop-v99.0.0", version = "99.0.0", asset = "penguin-remote-win-x64.exe";
const bytes = Buffer.from("local installer fixture");
const sha512 = crypto.createHash("sha512").update(bytes).digest("base64");
const feed = (v = version) => `version: ${v}\nfiles:\n  - url: ${asset}\n    sha512: ${sha512}\n    size: ${bytes.length}\npath: ${asset}\nsha512: ${sha512}\n`;
const cleanup = [];

afterEach(async () => {
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
  for (const clean of cleanup.splice(0).reverse()) await clean();
});

async function fixture({ primary = "github", feedFailures = [], packageFailures = [], wrongVersion, corrupt } = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "remote-source-http-"));
  cleanup.push(async () => {
    if (!path.resolve(root).startsWith(path.join(path.resolve(os.tmpdir()), "remote-source-http-"))) throw new Error("Unsafe test cleanup path");
    await fs.rm(root, { recursive: true, force: true });
  });
  generateReleaseDownloadManifest(root, tag);
  const manifest = await fs.readFile(path.join(root, "release-download-manifest.tsv"));
  const requests = [];
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, "http://localhost");
    requests.push(url.pathname);
    const send = (body, status = 200) => {
      res.writeHead(status, { "content-length": Buffer.byteLength(body), "cache-control": "no-store" }); res.end(body);
    };
    if (url.pathname === "/github-latest") return send(JSON.stringify({ tag_name: tag, draft: false, prerelease: false }));
    if (url.pathname === "/oss-latest") return send(JSON.stringify({ schemaVersion: 1, version, tag, releaseBaseUrl: `${OSS_RELEASE_ROOT}/${tag}` }));
    const match = /^\/(github|oss)\/([^/]+)\/([^/]+)$/.exec(url.pathname);
    if (!match || match[2] !== tag) return send("missing", 404);
    const [, source, , name] = match;
    if (name === "release-download-manifest.tsv") return send(manifest);
    if (name === "probe-64k.bin") return send(makeProbe(65536), primary === "oss" && source === "github" ? 503 : 200);
    if (name === "probe-1m.bin") return send(makeProbe(1048576));
    if (name === "latest.yml") return send(feed(wrongVersion === source ? "99.0.1" : version), feedFailures.includes(source) ? 404 : 200);
    if (name === asset) return send(corrupt === source ? Buffer.alloc(bytes.length, 0) : bytes, packageFailures.includes(source) ? 503 : 200);
    return send("missing", 404);
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  cleanup.push(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  const origin = `http://127.0.0.1:${server.address().port}`;
  const localURL = url => {
    if (url === GITHUB_LATEST_URL) return origin + "/github-latest";
    if (url === OSS_LATEST_URL) return origin + "/oss-latest";
    if (url.startsWith(GITHUB_RELEASE_ROOT + "/")) return url.replace(GITHUB_RELEASE_ROOT, origin + "/github");
    if (url.startsWith(OSS_RELEASE_ROOT + "/")) return url.replace(OSS_RELEASE_ROOT, origin + "/oss");
    throw new Error("Unexpected external URL: " + url);
  };
  const config = path.join(root, "app-update.yml");
  await fs.writeFile(config, `provider: generic\nurl: ${origin}/github/${tag}/\nupdaterCacheDirName: cache\n`);
  const app = { version: "0.1.7", name: "TestRemote", isPackaged: true, appUpdateConfigPath: config,
    userDataPath: root, baseCachePath: root, whenReady: async () => {}, quit() {}, relaunch() {}, onQuit() {} };
  const updater = new WindowsUpdater(null, app);
  updater.logger = null;
  updater.disableDifferentialDownload = true;
  // Keep the real downloader, YAML provider, checksum pipeline and cache. Replace
  // Electron's request creation with Node's HTTP transport and fake Authenticode.
  const executor = new ElectronHttpExecutor();
  vi.spyOn(executor, "createRequest").mockImplementation((options, callback) => http.request(options, callback));
  updater.httpExecutor = executor;
  updater.verifyUpdateCodeSignature = vi.fn(async () => null);
  const setFeedURL = updater.setFeedURL.bind(updater);
  vi.spyOn(updater, "setFeedURL").mockImplementation(options => {
    if (options.provider !== "generic") throw new Error("Test expects a pinned feed");
    setFeedURL({ ...options, url: localURL(options.url) });
  });
  const downloaded = vi.fn(); updater.on("update-downloaded", downloaded);
  const install = vi.fn(async () => {});
  const controller = new UpdateController(updater, app.version, undefined, install, vi.fn(), () => selectUpdateFeed({
    fetchFn: (url, init) => fetch(localURL(url), init), log: vi.fn(),
  }));
  cleanup.push(async () => controller.close());
  return { updater, controller, requests, downloaded, install };
}

describe.skipIf(process.platform !== "win32")("real updater with local HTTP sources", () => {
  it.each(["github", "oss"])("downloads from %s without installing automatically", async primary => {
    const f = await fixture({ primary });
    await f.controller.command("check");
    expect(f.controller.snapshot()).toMatchObject({ state: "available", version });
    expect(f.requests.some(url => url.endsWith(asset))).toBe(false);
    await f.controller.command("download");
    expect(f.controller.snapshot()).toMatchObject({ state: "downloaded", version });
    expect(await fs.readFile(f.updater.installerPath)).toEqual(bytes);
    expect(f.requests.filter(url => url.endsWith(asset))).toEqual([`/${primary}/${tag}/${asset}`]);
    expect(f.downloaded).toHaveBeenCalledOnce(); expect(f.install).not.toHaveBeenCalled();
  });
  it.each(["github", "oss"])("recovers when the %s package transfer fails", async primary => {
    const f = await fixture({ primary, packageFailures: [primary] });
    await f.controller.command("check"); await f.controller.command("download");
    expect(f.controller.snapshot()).toMatchObject({ state: "downloaded", version });
    expect(f.requests.filter(url => url.endsWith(asset))).toEqual([
      `/${primary}/${tag}/${asset}`, `/${primary === "github" ? "oss" : "github"}/${tag}/${asset}`,
    ]);
    expect(f.downloaded).toHaveBeenCalledOnce();
  });
  it("recovers from a missing primary update feed", async () => {
    const f = await fixture({ primary: "oss", feedFailures: ["oss"] });
    await f.controller.command("check"); await f.controller.command("download");
    expect(f.controller.snapshot().state).toBe("downloaded");
    expect(f.requests.filter(url => url.endsWith("latest.yml"))).toEqual([`/oss/${tag}/latest.yml`, `/github/${tag}/latest.yml`]);
    expect(f.requests.filter(url => url.endsWith(asset))).toEqual([`/github/${tag}/${asset}`]);
  });
  it("rejects a different fallback version before requesting its package", async () => {
    const f = await fixture({ packageFailures: ["github"], wrongVersion: "oss" });
    await f.controller.command("check"); await f.controller.command("download");
    expect(f.controller.snapshot()).toMatchObject({ state: "error", step: "check" });
    expect(f.requests.filter(url => url.endsWith(asset))).toEqual([`/github/${tag}/${asset}`]);
    expect(f.downloaded).not.toHaveBeenCalled();
  });
  it("stops at a real SHA-512 mismatch without downloading from the fallback", async () => {
    const f = await fixture({ corrupt: "github" });
    await f.controller.command("check"); await f.controller.command("download");
    expect(f.controller.snapshot()).toMatchObject({ state: "error", step: "check" });
    expect(f.requests.filter(url => url.endsWith(asset))).toEqual([`/github/${tag}/${asset}`]);
    expect(f.downloaded).not.toHaveBeenCalled();
  });
});

describe("platform-specific generic feeds", () => {
  it.each([
    ["win32", "x64", "latest.yml"],
    ["win32", "arm64", "latest.yml"],
    ["darwin", "x64", "latest-mac.yml"],
    ["darwin", "arm64", "latest-mac.yml"],
    ["linux", "x64", "latest-linux.yml"],
    ["linux", "arm64", "latest-linux-arm64.yml"],
  ])("requests the correct channel for %s %s", async (platform, arch, file) => {
    vi.stubEnv("TEST_UPDATER_ARCH", arch);
    const request = vi.fn(async () => feed());
    const provider = new GenericProvider({ provider: "generic", url: `${OSS_RELEASE_ROOT}/${tag}/` }, { channel: null, isAddNoCacheQuery: false }, { platform, executor: { request } });
    const info = await provider.getLatestVersion();
    expect(info.version).toBe(version);
    expect(request.mock.calls[0][0].path).toBe(`/releases/${tag}/${file}`);
  });
  it.each([false, true])("keeps the native macOS ZIP architecture selection (arm64: %s)", arm64 => {
    const files = ["x64", "arm64"].map(arch => ({ url: new URL(`${OSS_RELEASE_ROOT}/${tag}/penguin-remote-mac-${arch}.zip`), info: { url: `penguin-remote-mac-${arch}.zip`, sha512 } }));
    const filtered = electronUpdater.MacUpdater.filterFilesForArch(files, arm64);
    expect(findFile(filtered, "zip").url.pathname).toBe(`/releases/${tag}/penguin-remote-mac-${arm64 ? "arm64" : "x64"}.zip`);
  });
});
