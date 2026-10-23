import { createHash } from "node:crypto";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  GITHUB_FEED, GITHUB_LATEST_URL, GITHUB_MIN_BPS, GITHUB_RELEASE_ROOT, OSS_LATEST_URL,
  OSS_RELEASE_ROOT, githubFeed, ossFeed, parseProbeManifest, selectMeasuredSource, selectUpdateFeed,
} from "../src/update-source.js";
import type { FetchFn } from "../src/update-source.js";

const TAG = "desktop-v0.1.8", VERSION = "0.1.8";
const SMALL = Buffer.alloc(65536, 0xa5), LARGE = Buffer.alloc(1048576, 0x5a);
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const manifest = (tag = TAG) => `penguin-release-download-manifest\t1\t${tag}\n` +
  `probe\tsmall\tprobe-64k.bin\t65536\t${hash(SMALL)}\n` +
  `probe\tlarge\tprobe-1m.bin\t1048576\t${hash(LARGE)}\n`;
const pointer = (tag = TAG, version = VERSION) => JSON.stringify({ schemaVersion: 1, tag, version, releaseBaseUrl: `${OSS_RELEASE_ROOT}/${tag}` });
const github = (tag = TAG) => JSON.stringify({ tag_name: tag, draft: false, prerelease: false });
const gh = `${GITHUB_RELEASE_ROOT}/${TAG}`, oss = `${OSS_RELEASE_ROOT}/${TAG}`;

interface Route { body?: string | Buffer; status?: number; encoding?: string; ms?: number; response?: Response; error?: boolean }
function fixture() {
  const routes: Record<string, Route> = {
    [GITHUB_LATEST_URL]: { body: github() }, [OSS_LATEST_URL]: { body: pointer() },
    [`${oss}/release-download-manifest.tsv`]: { body: manifest() },
    [`${gh}/probe-64k.bin`]: { body: SMALL }, [`${oss}/probe-64k.bin`]: { body: SMALL },
    [`${gh}/probe-1m.bin`]: { body: LARGE, ms: 1000 }, [`${oss}/probe-1m.bin`]: { body: LARGE, ms: 1000 },
  };
  const clock = { now: Date.now() };
  vi.spyOn(Date, "now").mockImplementation(() => clock.now);
  const fetchFn = vi.fn<FetchFn>(async (url, init) => {
    const route = routes[url];
    if (route?.error) throw new Error("offline");
    if (route?.ms) clock.now += route.ms;
    expect(init.headers["accept-encoding"]).toBe("identity");
    expect(init.cache).toBe("no-store");
    return route?.response ?? new Response(typeof route?.body === "string" ? route.body : route?.body ? new Uint8Array(route.body) : null, {
      status: route?.status ?? (route ? 200 : 404), headers: route?.encoding ? { "content-encoding": route.encoding } : {},
    });
  });
  const log = vi.fn();
  return { routes, clock, fetchFn, log, select: (budgetMs = 26000) => selectUpdateFeed({ fetchFn, log, budgetMs }) };
}

afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers(); });

describe("Client probe contract", () => {
  it("accepts the existing three-row manifest without desktop_asset rows", () => {
    expect(parseProbeManifest(manifest(), TAG)).toEqual({
      small: { name: "probe-64k.bin", size: 65536, sha256: hash(SMALL) },
      large: { name: "probe-1m.bin", size: 1048576, sha256: hash(LARGE) },
    });
  });
  it.each([
    (s: string) => s.replace(TAG, "desktop-v0.1.7"),
    (s: string) => "\uFEFF" + s,
    (s: string) => s.replace("probe-64k.bin", "../probe-64k.bin"),
    (s: string) => s.replace("65536", "65535"),
    (s: string) => s.replace(hash(SMALL), "invalid"),
    (s: string) => s + "desktop_asset\tinstaller.exe\t123\thash\n",
    (s: string) => s.replace("probe\tlarge", "probe\tsmall"),
    (s: string) => s.trimEnd(),
  ])("rejects incompatible manifests (%#)", change => {
    expect(parseProbeManifest(change(manifest()), TAG)).toBeNull();
  });
});

describe("measured source policy", () => {
  it.each([
    [GITHUB_MIN_BPS, 10_000_000, "github"], [200_000, 300_000, "github"],
    [200_000, 300_001, "oss"], [0, 1, "oss"], [0, 0, "github"],
  ])("compares GitHub %s and OSS %s B/s", (ghSpeed, ossSpeed, selected) => {
    expect(selectMeasuredSource(ghSpeed, ossSpeed)).toBe(selected);
  });
  it("skips the OSS large probe when GitHub meets 256 KiB/s", async () => {
    const f = fixture();
    expect(await f.select()).toEqual({ version: VERSION, primary: githubFeed(TAG), fallback: ossFeed(TAG) });
    expect(f.fetchFn.mock.calls.some(([url]) => url === `${oss}/probe-1m.bin`)).toBe(false);
  });
  it("selects OSS when GitHub is slow and OSS is clearly faster", async () => {
    const f = fixture();
    f.routes[`${gh}/probe-1m.bin`]!.ms = 5000;
    expect(await f.select()).toEqual({ version: VERSION, primary: ossFeed(TAG), fallback: githubFeed(TAG) });
  });
  it("keeps GitHub when OSS is only slightly faster", async () => {
    const f = fixture();
    f.routes[`${gh}/probe-1m.bin`]!.ms = 5000;
    f.routes[`${oss}/probe-1m.bin`]!.ms = 4500;
    expect((await f.select()).primary).toEqual(githubFeed(TAG));
  });
  it.each(["github", "oss"] as const)("uses the other source if %s is unreachable", async source => {
    const f = fixture();
    delete f.routes[`${source === "github" ? gh : oss}/probe-64k.bin`];
    expect((await f.select()).primary).toEqual(source === "github" ? ossFeed(TAG) : githubFeed(TAG));
    expect(f.fetchFn.mock.calls.some(([url]) => url.endsWith("probe-1m.bin"))).toBe(false);
  });
  it.each([
    { body: Buffer.alloc(65536, 0x01) }, { body: SMALL.subarray(0, 65535) },
    { body: SMALL, encoding: "gzip" }, { body: SMALL, status: 503 }, { error: true },
    { body: Buffer.alloc(65537) },
  ])("rejects corrupt, compressed, oversized and failed probes (%#)", async route => {
    const f = fixture();
    f.routes[`${gh}/probe-64k.bin`] = route;
    expect((await f.select()).primary).toEqual(ossFeed(TAG));
  });
  it("keeps GitHub when neither small probe can be verified", async () => {
    const f = fixture();
    delete f.routes[`${gh}/probe-64k.bin`]; delete f.routes[`${oss}/probe-64k.bin`];
    expect((await f.select()).primary).toEqual(githubFeed(TAG));
  });
  it("measures OSS when the GitHub large probe fails", async () => {
    const f = fixture(); f.routes[`${gh}/probe-1m.bin`] = { status: 500 };
    expect((await f.select()).primary).toEqual(ossFeed(TAG));
  });
});

describe("release discovery and budgets", () => {
  it("keeps a newer GitHub version when the mirror lags and does not probe the old tag", async () => {
    const f = fixture(); f.routes[OSS_LATEST_URL] = { body: pointer("desktop-v0.1.7", "0.1.7") };
    expect(await f.select()).toEqual({ version: VERSION, primary: githubFeed(TAG) });
    expect(f.fetchFn).toHaveBeenCalledTimes(2);
  });
  it("uses GitHub even if the OSS pointer claims a newer version", async () => {
    const f = fixture(); f.routes[OSS_LATEST_URL] = { body: pointer("desktop-v0.1.9", "0.1.9") };
    expect(await f.select()).toEqual({ version: VERSION, primary: githubFeed(TAG) });
  });
  it.each([{ status: 403 }, { status: 500 }, { error: true }, { body: "{" }])("can discover from OSS when GitHub is unavailable (%#)", async route => {
    const f = fixture(); f.routes[GITHUB_LATEST_URL] = route;
    delete f.routes[`${gh}/probe-64k.bin`];
    expect(await f.select()).toEqual({ version: VERSION, primary: ossFeed(TAG), fallback: githubFeed(TAG) });
  });
  it.each([
    { schemaVersion: 2 }, { tag: "../desktop-v0.1.8" }, { version: "0.1.9" },
    { tag: "desktop-v0.1.8-staging.1" }, { releaseBaseUrl: "https://evil.example/releases/desktop-v0.1.8" },
    { releaseBaseUrl: `${OSS_RELEASE_ROOT}/${TAG}/` }, { tag: "desktop-v9007199254740992.1.8" },
  ])("rejects invalid mirror metadata (%#)", async changes => {
    const f = fixture(); delete f.routes[GITHUB_LATEST_URL];
    f.routes[OSS_LATEST_URL] = { body: JSON.stringify({ ...JSON.parse(pointer()), ...changes }) };
    expect(await f.select()).toEqual({ primary: GITHUB_FEED });
  });
  it.each([{ draft: true }, { prerelease: true }, { tag_name: "desktop-v0.1.8-beta.1" }, { tag_name: "v0.1.8" }])("rejects unsuitable GitHub releases (%#)", async changes => {
    const f = fixture(); delete f.routes[OSS_LATEST_URL];
    f.routes[GITHUB_LATEST_URL] = { body: JSON.stringify({ ...JSON.parse(github()), ...changes }) };
    expect(await f.select()).toEqual({ primary: GITHUB_FEED });
  });
  it("falls back to GitHub's manifest if the OSS copy is missing or malformed", async () => {
    const f = fixture();
    f.routes[`${oss}/release-download-manifest.tsv`] = { body: "invalid" };
    f.routes[`${gh}/release-download-manifest.tsv`] = { body: manifest() };
    expect((await f.select()).primary).toEqual(githubFeed(TAG));
    expect(f.fetchFn.mock.calls.some(([url]) => url === `${gh}/release-download-manifest.tsv`)).toBe(true);
  });
  it("supports an older release without probes through ordinary feed validation", async () => {
    const f = fixture(); delete f.routes[`${oss}/release-download-manifest.tsv`];
    expect(await f.select()).toEqual({ version: VERSION, primary: githubFeed(TAG), fallback: ossFeed(TAG) });
    expect(f.fetchFn.mock.calls.some(([url]) => url.endsWith(".bin"))).toBe(false);
  });
  it("starts no requests after the shared budget is exhausted", async () => {
    const f = fixture(); expect(await f.select(0)).toEqual({ primary: GITHUB_FEED });
    expect(f.fetchFn).not.toHaveBeenCalled();
  });
  it("aborts stalled headers within the shared budget", async () => {
    vi.useFakeTimers();
    const signals: AbortSignal[] = [];
    const fetchFn: FetchFn = async (_url, init) => { signals.push(init.signal); return new Promise(() => {}); };
    const result = selectUpdateFeed({ fetchFn, log: vi.fn(), budgetMs: 50 });
    await vi.advanceTimersByTimeAsync(50);
    expect(await result).toEqual({ primary: GITHUB_FEED });
    expect(signals).toHaveLength(2); expect(signals.every(signal => signal.aborted)).toBe(true);
  });
  it("cancels stalled response bodies within the budget", async () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    const fetchFn: FetchFn = async () => new Response(new ReadableStream({ cancel }));
    const result = selectUpdateFeed({ fetchFn, log: vi.fn(), budgetMs: 50 });
    await vi.advanceTimersByTimeAsync(50);
    expect(await result).toEqual({ primary: GITHUB_FEED });
    expect(cancel).toHaveBeenCalledTimes(2);
  });
});
