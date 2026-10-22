import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { sha256 } from "./verify-release-mirror.mjs";

export function command(binary, args) {
  const result = spawnSync(binary, args, { encoding: "utf8", maxBuffer: 16 * 1024 * 1024 });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${binary} failed: ${result.stderr || result.stdout}`);
  return result.stdout;
}

export function ossConfig(env = process.env) {
  for (const key of ["OSS_BUCKET", "OSS_REGION", "OSS_ENDPOINT", "OSS_PUBLIC_BASE_URL"]) {
    if (!env[key]) throw new Error("Missing Environment variable: " + key);
  }
  if (!/^[a-z0-9][a-z0-9-]+[a-z0-9]$/.test(env.OSS_BUCKET) || !/^[a-z0-9-]+$/.test(env.OSS_REGION) ||
      !/^[a-z0-9.-]+$/.test(env.OSS_ENDPOINT)) throw new Error("Invalid OSS bucket/region/endpoint");
  const url = new URL(env.OSS_PUBLIC_BASE_URL);
  if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash || url.pathname !== "/") throw new Error("OSS_PUBLIC_BASE_URL must be a bucket HTTPS origin");
  return { bucket: env.OSS_BUCKET, region: env.OSS_REGION, endpoint: env.OSS_ENDPOINT, publicBase: url.origin };
}

// No listing or deletion: every request addresses an exact object key.
export function ossClient(config, scratch, run = command, binary = process.env.OSSUTIL_BIN || "ossutil") {
  const options = ["--endpoint", config.endpoint, "--region", config.region];
  let sequence = 0;
  return {
    get(key) {
      const destination = path.join(scratch, `get-${sequence++}`);
      try {
        run(binary, ["cp", `oss://${config.bucket}/${key}`, destination, ...options, "--force", "--no-progress"]);
        return fs.readFileSync(destination);
      } catch (error) {
        if (/NoSuchKey/.test(error.message)) return null;
        throw error;
      } finally {
        fs.rmSync(destination, { force: true });
      }
    },
    put(key, bytes, { immutable = false, cache = "no-cache", contentType } = {}) {
      const source = path.join(scratch, `put-${sequence++}`);
      fs.writeFileSync(source, bytes);
      try {
        run(binary, ["api", "put-object", "--bucket", config.bucket, "--key", key,
          "--body", `file://${source}`, ...(immutable ? ["--forbid-overwrite"] : []),
          ...(contentType ? ["--content-type", contentType] : []),
          "--cache-control", cache, ...options]);
      } finally {
        fs.rmSync(source, { force: true });
      }
    },
  };
}

export function sameBytes(actual, expected, key) {
  if (actual === null || sha256(actual) !== sha256(expected)) throw new Error("OSS content mismatch: " + key);
}

export function githubClient(repository = process.env.GITHUB_REPOSITORY, run = command) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository || "")) throw new Error("GITHUB_REPOSITORY is required");
  return {
    release: tag => JSON.parse(run("gh", ["api", `repos/${repository}/releases/tags/${encodeURIComponent(tag)}`])),
    latest() {
      try { return JSON.parse(run("gh", ["api", `repos/${repository}/releases/latest`])); }
      catch (error) {
        if (/HTTP 404/.test(error.message)) return null;
        throw error;
      }
    },
    download(tag, directory) {
      run("gh", ["release", "download", tag, "--repo", repository, "--dir", directory]);
    },
  };
}
