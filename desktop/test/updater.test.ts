import { EventEmitter } from "node:events";
import { afterEach, describe, expect, it, vi } from "vitest";
import { newerStableVersion, UpdateController, updateUnsupportedReason } from "../src/update-controller.js";
import { startUpdateBridge } from "../src/update-bridge.js";
import { WindowsUpdateVerificationError } from "../src/update-errors.js";

class FakeUpdater extends EventEmitter {
  autoDownload = true;
  autoInstallOnAppQuit = true;
  allowPrerelease = true;
  allowDowngrade = true;
  checkForUpdates = vi.fn(async () => ({ isUpdateAvailable: true, updateInfo: { version: "0.1.6" } }));
  downloadUpdate = vi.fn(async () => {});
}
function setup(reason?: string) {
  const updater = new FakeUpdater(), install = vi.fn(async () => {}), log = vi.fn();
  const controller = new UpdateController(updater, "0.1.5", reason, install, log);
  return { updater, install, controller };
}
afterEach(() => vi.useRealTimers());

describe("desktop updater", () => {
  it("checks quietly and requires separate download and installation actions", async () => {
    const { updater, install, controller } = setup();
    expect(updater.autoDownload).toBe(false);
    expect(updater.autoInstallOnAppQuit).toBe(false);
    expect(updater.allowPrerelease).toBe(false);
    expect(updater.allowDowngrade).toBe(false);
    await controller.command("install");
    await controller.command("download");
    expect(install).not.toHaveBeenCalled();
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
    await controller.command("check");
    expect(controller.snapshot()).toMatchObject({ state: "available", version: "0.1.6" });
    await controller.command("download");
    expect(install).not.toHaveBeenCalled();
    await controller.command("check");
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1);
    expect(controller.snapshot().state).toBe("downloaded");
    await controller.command("install");
    expect(install).toHaveBeenCalledOnce();
  });

  it("deduplicates downloads, keeps progress, and permits retry after failure", async () => {
    const { updater, controller } = setup();
    await controller.command("check");
    let finish!: () => void;
    updater.downloadUpdate.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; }));
    const downloading = controller.command("download");
    await controller.command("download");
    await controller.command("check");
    updater.emit("download-progress", { percent: 40.2 });
    expect(controller.snapshot()).toMatchObject({ state: "downloading", percent: 40 });
    finish(); await downloading;
    expect(updater.downloadUpdate).toHaveBeenCalledOnce();
    updater.emit("error", new Error("late check failure"));
    expect(controller.snapshot().state).toBe("downloaded");

    const other = setup();
    await other.controller.command("check");
    other.updater.downloadUpdate.mockRejectedValueOnce(new Error("offline"));
    await other.controller.command("download");
    expect(other.controller.snapshot()).toMatchObject({ state: "error", step: "download", version: "0.1.6" });
    await other.controller.command("download");
    expect(other.controller.snapshot().state).toBe("downloaded");
  });

  it("preserves a downloaded package when restarting fails", async () => {
    const { controller, install, updater } = setup();
    await controller.command("check"); await controller.command("download");
    install.mockRejectedValueOnce(new Error("child still running"));
    await controller.command("install");
    expect(controller.snapshot()).toMatchObject({ state: "downloaded", step: "install" });
    await controller.command("install");
    updater.emit("error", new Error("native install failed"));
    expect(controller.snapshot()).toMatchObject({ state: "downloaded", step: "install" });
  });

  it("returns to checking when Windows invalidates a cached installer", async () => {
    const { controller, updater, install } = setup();
    await controller.command("check"); await controller.command("download");
    await controller.command("install");
    updater.emit("error", new WindowsUpdateVerificationError());
    expect(controller.snapshot()).toMatchObject({ state: "error", step: "install" });
    expect(controller.snapshot().version).toBeUndefined();
    await controller.command("install");
    expect(install).toHaveBeenCalledOnce();
    await controller.command("check"); await controller.command("download");
    expect(controller.snapshot().state).toBe("downloaded");
  });

  it("schedules checks after startup and stops them on shutdown", async () => {
    vi.useFakeTimers();
    const { updater, controller } = setup();
    updater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: false, updateInfo: { version: "0.1.5" } });
    controller.start(); controller.start();
    await vi.advanceTimersByTimeAsync(19_999);
    expect(updater.checkForUpdates).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(updater.checkForUpdates).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    controller.close();
    await vi.advanceTimersByTimeAsync(6 * 60 * 60 * 1000);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it("isolates previews, unsupported architectures and package-manager installs", async () => {
    expect(updateUnsupportedReason(false, "0.1.5", "win32", "x64")).toBe("dev");
    expect(updateUnsupportedReason(true, "0.1.6-staging.1", "win32", "x64")).toBe("preview");
    expect(updateUnsupportedReason(true, "0.1.5", "linux", "x64")).toBe("linux-not-appimage");
    expect(updateUnsupportedReason(true, "0.1.5", "linux", "arm64", "app")).toBe("platform");
    expect(updateUnsupportedReason(true, "0.1.5", "darwin", "arm64")).toBeUndefined();
    expect(updateUnsupportedReason(true, "0.1.5", "linux", "x64", "app")).toBeUndefined();
    const { controller, updater } = setup("preview");
    await controller.command("check");
    expect(updater.checkForUpdates).not.toHaveBeenCalled();
    expect(newerStableVersion("0.1.10", "0.1.5")).toBe(true);
    expect(newerStableVersion("0.1.6-beta.1", "0.1.5")).toBe(false);
    expect(newerStableVersion("0.1.4", "0.1.5")).toBe(false);
  });

  it("respects the updater's system-version and rollout eligibility decision", async () => {
    const { updater, controller } = setup();
    updater.checkForUpdates.mockResolvedValue({ isUpdateAvailable: false, updateInfo: { version: "0.1.6" } });
    await controller.command("check");
    expect(controller.snapshot().state).toBe("up-to-date");
    await controller.command("download");
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
  });
});

describe("private updater bridge", () => {
  it("rejects missing credentials, browser origins and unknown commands", async () => {
    const { controller, updater } = setup();
    const bridge = await startUpdateBridge(controller);
    try {
      expect((await fetch(bridge.url + "/status")).status).toBe(403);
      const headers = { authorization: "Bearer " + bridge.secret };
      expect((await fetch(bridge.url + "/status", { headers: { ...headers, origin: "https://example.com" } })).status).toBe(403);
      expect((await fetch(bridge.url + "/arbitrary", { method: "POST", headers })).status).toBe(404);
      expect((await fetch(bridge.url + "/install", { headers })).status).toBe(404);
      expect((await fetch(bridge.url + "/check", { method: "POST", headers })).status).toBe(202);
      expect(await (await fetch(bridge.url + "/status", { headers })).json()).toMatchObject({ state: "available" });
      expect(updater.checkForUpdates).toHaveBeenCalledOnce();
    } finally { bridge.close(); }
  });
});
