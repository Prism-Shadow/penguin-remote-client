import path from "node:path";
import crypto from "node:crypto";

export interface DesktopBuildConfig {
  penguinGoUrl: string;
}

export function configPathForService(userData: string, baseURL: string): string {
  const target = new URL(baseURL);
  if (target.username || target.password || target.search || target.hash) {
    throw new Error("Penguin Go URL must not contain credentials, query or fragment");
  }
  const identity = target.origin + target.pathname.replace(/\/+$/, "");
  const key = crypto.createHash("sha256").update(identity).digest("hex");
  return path.join(userData, "services", key, "remote.json");
}

export function readyURLFromOutput(output: string): string | null {
  const match = output.match(/Penguin Remote client is ready:\s+(http:\/\/[^\s]+)/);
  return match?.[1] ?? null;
}

export function remoteBinaryName(platform: NodeJS.Platform): string {
  return platform === "win32" ? "penguin-remote.exe" : "penguin-remote";
}

export function remoteBinaryPath(
  appPath: string,
  resourcesPath: string,
  platform: NodeJS.Platform,
  packaged: boolean,
): string {
  const base = packaged ? path.join(resourcesPath, "bin") : path.join(appPath, "native");
  return path.join(base, remoteBinaryName(platform));
}

export function isLocalAppURL(target: string, origin: string): boolean {
  try {
    return new URL(target).origin === new URL(origin).origin;
  } catch {
    return false;
  }
}
