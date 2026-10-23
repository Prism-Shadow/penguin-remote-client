import { createHash } from "node:crypto";

export const GITHUB_LATEST_URL = "https://api.github.com/repos/Prism-Shadow/penguin-remote-client/releases/latest";
export const GITHUB_RELEASE_ROOT = "https://github.com/Prism-Shadow/penguin-remote-client/releases/download";
export const OSS_ORIGIN = "https://penguin-remote-client-releases.oss-cn-beijing.aliyuncs.com";
export const OSS_RELEASE_ROOT = `${OSS_ORIGIN}/releases`;
export const OSS_LATEST_URL = `${OSS_ORIGIN}/latest.json`;
export const PROBE_BUDGET_MS = 26_000;
export const GITHUB_MIN_BPS = 262144;
export const OSS_SWITCH_RATIO = 1.5;

export type UpdateFeed =
  | { provider: "github"; owner: string; repo: string; tagNamePrefix: string }
  | { provider: "generic"; url: string };
export const GITHUB_FEED: UpdateFeed = {
  provider: "github", owner: "Prism-Shadow", repo: "penguin-remote-client", tagNamePrefix: "desktop-v",
};
export interface FeedSelection {
  primary: UpdateFeed;
  fallback?: UpdateFeed;
  version?: string;
}
export type FetchFn = (url: string, init: {
  headers: Record<string, string>; signal: AbortSignal; cache: "no-store";
}) => Promise<Pick<Response, "ok" | "headers" | "body">>;

interface ReleaseTarget { tag: string; version: string }
interface Probe { name: string; size: number; sha256: string }
export interface ProbeManifest { small: Probe; large: Probe }

function stableTarget(tag: unknown): ReleaseTarget | null {
  if (typeof tag !== "string" || !/^desktop-v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(tag)) return null;
  const version = tag.slice("desktop-v".length);
  return version.split(".").every(part => Number.isSafeInteger(Number(part))) ? { tag, version } : null;
}

export function githubFeed(tag: string): UpdateFeed {
  return { provider: "generic", url: `${GITHUB_RELEASE_ROOT}/${tag}/` };
}
export function ossFeed(tag: string): UpdateFeed {
  return { provider: "generic", url: `${OSS_RELEASE_ROOT}/${tag}/` };
}

/** Bound both time and bytes, including a stalled response body. */
async function fetchBytes(fetchFn: FetchFn, url: string, deadline: number, limitMs: number,
  maxBytes: number, identity = false): Promise<Buffer | null> {
  const remaining = Math.min(limitMs, deadline - Date.now());
  if (remaining <= 0) return null;
  const controller = new AbortController();
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  let timeout: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      (async () => {
        const response = await fetchFn(url, {
          headers: { "accept-encoding": "identity", accept: identity ? "application/octet-stream" : "application/json, text/plain" },
          signal: controller.signal, cache: "no-store",
        });
        if (!response.ok || controller.signal.aborted) return null;
        const encoding = response.headers.get("content-encoding");
        if (identity && encoding !== null && encoding.toLowerCase() !== "identity") return null;
        if (response.body === null) return null;
        reader = response.body.getReader();
        const chunks: Uint8Array[] = [];
        let size = 0;
        for (;;) {
          if (controller.signal.aborted) return null;
          const chunk = await reader.read();
          if (chunk.done) return Buffer.concat(chunks, size);
          size += chunk.value.byteLength;
          if (size > maxBytes) return null;
          chunks.push(chunk.value);
        }
      })(),
      new Promise<null>(resolve => {
        timeout = setTimeout(() => { controller.abort(); resolve(null); }, remaining);
      }),
    ]);
  } catch {
    return null;
  } finally {
    clearTimeout(timeout);
    controller.abort();
    // A timed-out body must not hold the source-selection cycle open.
    void reader?.cancel().catch(() => {});
  }
}

async function latestTarget(fetchFn: FetchFn, source: "github" | "oss", deadline: number): Promise<ReleaseTarget | null> {
  const bytes = await fetchBytes(fetchFn, source === "github" ? GITHUB_LATEST_URL : OSS_LATEST_URL,
    deadline, 8_000, source === "github" ? 1024 * 1024 : 16 * 1024);
  try {
    const value: unknown = bytes === null ? null : JSON.parse(bytes.toString("utf8"));
    if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
    const data = value as Record<string, unknown>;
    if (source === "github") {
      if (data.draft !== false || data.prerelease !== false) return null;
      return stableTarget(data.tag_name);
    }
    const target = stableTarget(data.tag);
    if (target === null || data.schemaVersion !== 1 || data.version !== target.version ||
      data.releaseBaseUrl !== `${OSS_RELEASE_ROOT}/${target.tag}`) return null;
    return target;
  } catch { return null; }
}

/** Client v1 has exactly two probe rows, with no Harness desktop_asset rows. */
export function parseProbeManifest(text: string, tag: string): ProbeManifest | null {
  const lines = text.split("\n");
  if (lines.length !== 4 || lines[3] !== "" || lines[0] !== `penguin-release-download-manifest\t1\t${tag}`) return null;
  const probes: Probe[] = [];
  for (const [index, spec] of [
    { label: "small", name: "probe-64k.bin", size: 65536 },
    { label: "large", name: "probe-1m.bin", size: 1048576 },
  ].entries()) {
    const row = lines[index + 1]!.split("\t");
    if (row.length !== 5 || row[0] !== "probe" || row[1] !== spec.label || row[2] !== spec.name ||
      row[3] !== String(spec.size) || !/^[0-9a-f]{64}$/.test(row[4]!)) return null;
    probes.push({ name: spec.name, size: spec.size, sha256: row[4]! });
  }
  return { small: probes[0]!, large: probes[1]! };
}

async function loadManifest(fetchFn: FetchFn, tag: string, deadline: number): Promise<ProbeManifest | null> {
  for (const base of [OSS_RELEASE_ROOT, GITHUB_RELEASE_ROOT]) {
    const bytes = await fetchBytes(fetchFn, `${base}/${tag}/release-download-manifest.tsv`, deadline, 5_000, 4096);
    const manifest = bytes === null ? null : parseProbeManifest(bytes.toString("utf8"), tag);
    if (manifest !== null) return manifest;
  }
  return null;
}

async function probeSource(fetchFn: FetchFn, base: string, probe: Probe, deadline: number, limitMs: number): Promise<number | null> {
  const started = Date.now();
  const bytes = await fetchBytes(fetchFn, `${base}/${probe.name}`, deadline, limitMs, probe.size, true);
  if (bytes === null || bytes.length !== probe.size || createHash("sha256").update(bytes).digest("hex") !== probe.sha256) return null;
  return Math.floor(bytes.length * 1000 / Math.max(Date.now() - started, 1));
}

export function selectMeasuredSource(githubBps: number, ossBps: number): "github" | "oss" {
  return githubBps < GITHUB_MIN_BPS && ossBps > githubBps * OSS_SWITCH_RATIO ? "oss" : "github";
}

/** Discover the version first; a lagging mirror can never hide a GitHub release. */
export async function selectUpdateFeed(opts: {
  fetchFn: FetchFn; log: (message: string) => void; budgetMs?: number;
}): Promise<FeedSelection> {
  const deadline = Date.now() + (opts.budgetMs ?? PROBE_BUDGET_MS);
  const [github, oss] = await Promise.all([
    latestTarget(opts.fetchFn, "github", deadline), latestTarget(opts.fetchFn, "oss", deadline),
  ]);
  const target = github ?? oss;
  if (target === null) {
    opts.log("Source discovery unavailable; keeping the GitHub release provider.");
    return { primary: GITHUB_FEED };
  }
  const gh = githubFeed(target.tag), mirror = ossFeed(target.tag);
  const githubOnly: FeedSelection = { primary: gh, version: target.version };
  if (github === null) opts.log(`GitHub discovery unavailable; using OSS stable target ${target.tag}.`);
  if (oss?.tag !== target.tag) {
    opts.log(`OSS does not confirm ${target.tag}; using GitHub without mirror fallback.`);
    return githubOnly;
  }
  const manifest = await loadManifest(opts.fetchFn, target.tag, deadline);
  if (manifest === null) {
    opts.log("Source probes unavailable; keeping GitHub with same-version OSS fallback.");
    return { ...githubOnly, fallback: mirror };
  }
  const ghBase = `${GITHUB_RELEASE_ROOT}/${target.tag}`, ossBase = `${OSS_RELEASE_ROOT}/${target.tag}`;
  const [ghSmall, ossSmall] = await Promise.all([
    probeSource(opts.fetchFn, ghBase, manifest.small, deadline, 5_000),
    probeSource(opts.fetchFn, ossBase, manifest.small, deadline, 5_000),
  ]);
  let source: "github" | "oss" = "github";
  if (ghSmall === null && ossSmall !== null) source = "oss";
  else if (ghSmall !== null && ossSmall !== null) {
    const ghSpeed = await probeSource(opts.fetchFn, ghBase, manifest.large, deadline, 8_000) ?? 0;
    if (ghSpeed < GITHUB_MIN_BPS) {
      const ossSpeed = await probeSource(opts.fetchFn, ossBase, manifest.large, deadline, 8_000) ?? 0;
      source = selectMeasuredSource(ghSpeed, ossSpeed);
      opts.log(`Source speeds for ${target.tag}: GitHub ${ghSpeed} B/s, OSS ${ossSpeed} B/s.`);
    }
  }
  opts.log(`Selected ${source === "github" ? "GitHub" : "OSS"} for ${target.tag}.`);
  return { version: target.version, primary: source === "github" ? gh : mirror, fallback: source === "github" ? mirror : gh };
}
