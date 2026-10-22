import { WindowsUpdateVerificationError } from "./update-errors.js";

export type UpdateState = "idle" | "checking" | "up-to-date" | "available" | "downloading" | "downloaded" | "installing" | "error" | "unsupported";
export interface UpdateStatus {
  appVersion: string;
  state: UpdateState;
  version?: string;
  percent?: number;
  reason?: string;
  step?: "check" | "download" | "install";
}

export interface Updater {
  autoDownload: boolean;
  autoInstallOnAppQuit: boolean;
  allowPrerelease: boolean;
  allowDowngrade: boolean;
  on(event: "download-progress", listener: (progress: { percent: number }) => void): unknown;
  on(event: "error", listener: (error: Error) => void): unknown;
  checkForUpdates(): Promise<{ isUpdateAvailable: boolean; updateInfo: { version: string } } | null>;
  downloadUpdate(): Promise<unknown>;
}

export function updateUnsupportedReason(packaged: boolean, version: string, platform: string, arch: string, appImage?: string): string | undefined {
  if (!packaged) return "dev";
  // Preview builds must never switch the installation's service environment.
  if (!/^\d+\.\d+\.\d+$/.test(version)) return "preview";
  if (platform === "linux" && !appImage) return "linux-not-appimage";
  if ((platform === "win32" || platform === "linux") && arch === "x64") return;
  if (platform === "darwin" && (arch === "x64" || arch === "arm64")) return;
  return "platform";
}

/** One owner for checks, downloads and installation, independent of the window. */
export class UpdateController {
  private status: UpdateStatus;
  private busy = false;
  private timer?: ReturnType<typeof setInterval>;
  private firstCheck?: ReturnType<typeof setTimeout>;

  constructor(private updater: Updater, version: string, reason: string | undefined,
    private install: () => Promise<void>, private log: (message: string) => void) {
    this.status = { appVersion: version, state: reason ? "unsupported" : "idle", ...(reason ? { reason } : {}) };
    updater.autoDownload = false;
    updater.autoInstallOnAppQuit = false;
    updater.allowPrerelease = false;
    updater.allowDowngrade = false;
    updater.on("download-progress", ({ percent }: { percent: number }) => {
      if (this.status.state === "downloading" && Number.isFinite(percent)) {
        this.status = { ...this.status, percent: Math.max(0, Math.min(100, Math.round(percent))) };
      }
    });
    // Errors are handled by the awaited command; an EventEmitter error must still be consumed.
    updater.on("error", (error: Error) => {
      log("Updater error: " + error.message);
      if (error instanceof WindowsUpdateVerificationError) {
        this.status = { appVersion: this.status.appVersion, state: "error", step: "install" };
      } else if (this.status.state === "installing") {
        this.status = { ...this.status, state: "downloaded", step: "install" };
      }
    });
  }

  snapshot(): UpdateStatus { return { ...this.status }; }

  start(): void {
    if (this.status.state === "unsupported" || this.timer) return;
    this.firstCheck = setTimeout(() => void this.command("check"), 20_000);
    this.timer = setInterval(() => void this.command("check"), 6 * 60 * 60 * 1000);
    this.firstCheck.unref();
    this.timer.unref();
  }

  close(): void { clearTimeout(this.firstCheck); clearInterval(this.timer); }

  async command(action: "check" | "download" | "install"): Promise<void> {
    if (this.busy || this.status.state === "unsupported" || this.status.state === "installing") return;
    const previous = this.snapshot();
    if (action === "check" && ["available", "downloaded"].includes(previous.state)) return;
    if (action === "download" && previous.state !== "available" && !(previous.state === "error" && previous.step === "download")) return;
    if (action === "install" && previous.state !== "downloaded") return;
    if (action !== "check" && !previous.version) return;
    this.busy = true;
    const base = { appVersion: previous.appVersion };
    try {
      if (action === "check") {
        this.status = { ...base, state: "checking" };
        const result = await this.updater.checkForUpdates();
        const version = result?.updateInfo?.version;
        if (!version) throw new Error("Update feed returned no version");
        this.status = { ...base, state: result?.isUpdateAvailable && newerStableVersion(version, base.appVersion) ? "available" : "up-to-date", version };
      } else if (action === "download") {
        this.status = { ...base, state: "downloading", version: previous.version!, percent: 0 };
        await this.updater.downloadUpdate();
        this.status = { ...base, state: "downloaded", version: previous.version!, percent: 100 };
      } else {
        this.status = { ...base, state: "installing", version: previous.version! };
        await this.install();
      }
    } catch (error) {
      this.log(`${action} failed: ${String(error)}`);
      // A failed restart must leave the downloaded package available to retry.
      this.status = error instanceof WindowsUpdateVerificationError ? {
        ...base, state: "error", step: "install",
      } : action === "install" ? { ...previous, step: "install" } : {
        ...base, state: "error", step: action, ...(action === "download" ? { version: previous.version! } : {}),
      };
    } finally { this.busy = false; }
  }
}

export function newerStableVersion(candidate: string, current: string): boolean {
  if (!/^\d+\.\d+\.\d+$/.test(candidate)) return false;
  const left = candidate.split(".").map(Number), right = current.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if (left[i] !== right[i]) return left[i]! > right[i]!;
  }
  return false;
}
