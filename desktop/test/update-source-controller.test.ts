import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import { UpdateController } from "../src/update-controller.js";
import { UpdateIntegrityError } from "../src/update-errors.js";
import { githubFeed, ossFeed } from "../src/update-source.js";
import type { FeedSelection } from "../src/update-source.js";

const TAG = "desktop-v0.1.8", VERSION = "0.1.8";
const result = (version = VERSION, isUpdateAvailable = true) => ({ isUpdateAvailable, updateInfo: { version } });
const offline = () => Object.assign(new Error("connection failed"), { code: "ECONNRESET" });
const electronFailures = ["ERR_ADDRESS_UNREACHABLE", "ERR_HTTP2_PROTOCOL_ERROR", "ERR_CONTENT_LENGTH_MISMATCH"]
  .flatMap(code => [
    { label: `${code} property`, error: Object.assign(new Error("request failed"), { code }) },
    { label: `${code} message`, error: new Error(`net::${code}`) },
  ]);

function fixture(reverse = false) {
  class FakeUpdater extends EventEmitter {
    autoDownload = true;
    autoInstallOnAppQuit = true;
    allowPrerelease = true;
    allowDowngrade = true;
    setFeedURL = vi.fn();
    checkForUpdates = vi.fn(async () => result());
    downloadUpdate = vi.fn(async () => {});
  }
  const updater = new FakeUpdater(), install = vi.fn(async () => {}), log = vi.fn();
  const select = vi.fn(async (): Promise<FeedSelection> => ({ version: VERSION,
    primary: reverse ? ossFeed(TAG) : githubFeed(TAG), fallback: reverse ? githubFeed(TAG) : ossFeed(TAG),
  }));
  const controller = new UpdateController(updater, "0.1.7", undefined, install, log, select);
  return { updater, controller, install, log, select };
}

describe("same-release source fallback", () => {
  describe.each([false, true])("Electron transport failures (OSS primary: %s)", reverse => {
    it.each(electronFailures)("switches once on a failed check: $label", async ({ error }) => {
      const { updater, controller } = fixture(reverse);
      updater.checkForUpdates.mockRejectedValueOnce(error);
      await controller.command("check");
      expect(controller.snapshot()).toMatchObject({ state: "available", version: VERSION });
      expect(updater.setFeedURL.mock.calls.map(([feed]) => feed)).toEqual(reverse ? [ossFeed(TAG), githubFeed(TAG)] : [githubFeed(TAG), ossFeed(TAG)]);
      expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    });

    it.each(electronFailures)("rechecks the exact offered version before download fallback: $label", async ({ error }) => {
      const { updater, controller } = fixture(reverse);
      await controller.command("check");
      updater.downloadUpdate.mockRejectedValueOnce(error).mockImplementationOnce(async () => {
        expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
        expect(controller.snapshot()).toMatchObject({ state: "downloading", version: VERSION, percent: 0 });
      });
      await controller.command("download");
      expect(updater.setFeedURL.mock.calls.map(([feed]) => feed)).toEqual(reverse ? [ossFeed(TAG), githubFeed(TAG)] : [githubFeed(TAG), ossFeed(TAG)]);
      expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
      expect(controller.snapshot()).toMatchObject({ state: "downloaded", version: VERSION });
    });

    it("stops after both package sources report Electron transport failures", async () => {
      const { updater, controller } = fixture(reverse);
      await controller.command("check");
      updater.downloadUpdate.mockRejectedValue(new Error("net::ERR_CONTENT_LENGTH_MISMATCH"));
      await controller.command("download");
      expect(updater.setFeedURL).toHaveBeenCalledTimes(2);
      expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
      expect(controller.snapshot()).toMatchObject({ state: "error", step: "download", version: VERSION });
    });

    it("rejects a changed fallback version after an Electron transport failure", async () => {
      const { updater, controller } = fixture(reverse);
      await controller.command("check");
      updater.downloadUpdate.mockRejectedValueOnce(new Error("net::ERR_HTTP2_PROTOCOL_ERROR"));
      updater.checkForUpdates.mockResolvedValueOnce(result("0.1.9"));
      await controller.command("download");
      expect(updater.downloadUpdate).toHaveBeenCalledOnce();
      expect(controller.snapshot()).toMatchObject({ state: "error", step: "check" });
      expect(controller.snapshot().version).toBeUndefined();
    });
  });

  it.each([false, true])("retries a failed feed once in either direction (OSS primary: %s)", async reverse => {
    const { updater, controller, select } = fixture(reverse);
    updater.checkForUpdates.mockImplementationOnce(async () => {
      updater.emit("error", offline());
      expect(controller.snapshot().state).toBe("checking");
      throw offline();
    });
    await controller.command("check");
    expect(controller.snapshot()).toMatchObject({ state: "available", version: VERSION });
    expect(updater.setFeedURL.mock.calls.map(([feed]) => feed)).toEqual(reverse ? [ossFeed(TAG), githubFeed(TAG)] : [githubFeed(TAG), ossFeed(TAG)]);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(select).toHaveBeenCalledOnce();
    expect(updater.downloadUpdate).not.toHaveBeenCalled();
  });

  it.each([false, true])("rechecks the same version before retrying a package download (OSS primary: %s)", async reverse => {
    const { updater, controller } = fixture(reverse);
    await controller.command("check");
    updater.downloadUpdate.mockImplementationOnce(async () => {
      updater.emit("download-progress", { percent: 80 });
      updater.emit("error", offline());
      expect(controller.snapshot()).toMatchObject({ state: "downloading", percent: 80 });
      throw offline();
    }).mockImplementationOnce(async () => {
      expect(controller.snapshot()).toMatchObject({ state: "downloading", percent: 0, version: VERSION });
    });
    await controller.command("download");
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
    expect(controller.snapshot()).toMatchObject({ state: "downloaded", version: VERSION, percent: 100 });
  });

  it("does not switch back if checking already consumed the fallback", async () => {
    const { updater, controller } = fixture();
    updater.checkForUpdates.mockRejectedValueOnce(offline());
    await controller.command("check");
    updater.downloadUpdate.mockRejectedValue(offline());
    await controller.command("download");
    expect(updater.setFeedURL).toHaveBeenCalledTimes(2);
    expect(updater.downloadUpdate).toHaveBeenCalledOnce();
    expect(controller.snapshot()).toMatchObject({ state: "error", step: "download", version: VERSION });
    await controller.command("download");
    expect(updater.setFeedURL).toHaveBeenCalledTimes(2);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(3);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
  });

  it("reports failure after both feeds fail and starts a fresh cycle on user retry", async () => {
    const { updater, controller, select } = fixture();
    updater.checkForUpdates.mockRejectedValueOnce(offline()).mockRejectedValueOnce(offline());
    await controller.command("check");
    expect(controller.snapshot()).toMatchObject({ state: "error", step: "check" });
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    await controller.command("check");
    expect(select).toHaveBeenCalledTimes(2);
    expect(controller.snapshot().state).toBe("available");
  });

  it.each(["0.1.7", "0.1.9", "0.1.8-staging.1"])("refuses a fallback offering %s instead of the offered release", async version => {
    const { updater, controller } = fixture();
    await controller.command("check");
    updater.downloadUpdate.mockRejectedValueOnce(offline());
    updater.checkForUpdates.mockResolvedValueOnce(result(version));
    await controller.command("download");
    expect(updater.downloadUpdate).toHaveBeenCalledOnce();
    expect(controller.snapshot()).toMatchObject({ state: "error", step: "check" });
    expect(controller.snapshot().version).toBeUndefined();
    await controller.command("download");
    expect(updater.downloadUpdate).toHaveBeenCalledOnce();
    await controller.command("check");
    expect(controller.snapshot()).toMatchObject({ state: "available", version: VERSION });
  });

  it("rejects a wrong version returned by the primary feed", async () => {
    const { updater, controller } = fixture();
    updater.checkForUpdates.mockResolvedValueOnce(result("0.1.9"));
    await controller.command("check");
    expect(controller.snapshot()).toMatchObject({ state: "error", step: "check" });
    expect(updater.checkForUpdates).toHaveBeenCalledOnce();
    expect(updater.setFeedURL).toHaveBeenCalledOnce();
  });

  it("preserves eligibility decisions on primary and fallback feeds", async () => {
    const f = fixture();
    f.updater.checkForUpdates.mockResolvedValueOnce(result(VERSION, false));
    await f.controller.command("check");
    expect(f.controller.snapshot().state).toBe("up-to-date");
    await f.controller.command("download");
    expect(f.updater.downloadUpdate).not.toHaveBeenCalled();

    const other = fixture();
    await other.controller.command("check");
    other.updater.downloadUpdate.mockRejectedValueOnce(offline());
    other.updater.checkForUpdates.mockResolvedValueOnce(result(VERSION, false));
    await other.controller.command("download");
    expect(other.updater.downloadUpdate).toHaveBeenCalledOnce();
    expect(other.controller.snapshot()).toMatchObject({ state: "error", step: "check" });
  });

  it.each([
    Object.assign(new Error("sha512 checksum mismatch"), { code: "ERR_CHECKSUM_MISMATCH" }),
    Object.assign(new Error("not signed by owner"), { code: "ERR_UPDATER_INVALID_SIGNATURE" }),
    new UpdateIntegrityError("Windows update signature could not be verified."),
    Object.assign(new Error("bad update metadata"), { code: "ERR_UPDATER_INVALID_UPDATE_INFO" }),
  ])("does not retry integrity failures (%#)", async error => {
    const { updater, controller } = fixture();
    await controller.command("check");
    updater.downloadUpdate.mockRejectedValueOnce(error);
    await controller.command("download");
    expect(updater.downloadUpdate).toHaveBeenCalledOnce();
    expect(updater.setFeedURL).toHaveBeenCalledOnce();
    expect(controller.snapshot()).toMatchObject({ state: "error", step: "check" });
  });

  it("does not treat an unknown application error as a source failure", async () => {
    const { updater, controller } = fixture();
    updater.checkForUpdates.mockRejectedValueOnce(new Error("internal error"));
    await controller.command("check");
    expect(updater.checkForUpdates).toHaveBeenCalledOnce();
  });

  it.each([
    Object.assign(new Error("Not found"), { code: "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND" }),
    Object.assign(new Error("Server failed"), { statusCode: 503 }),
    new Error("net::ERR_CONNECTION_RESET"),
    Object.assign(new Error("Missing asset"), { statusCode: 404 }),
    new Error('Cannot download "https://example.com/installer.exe", status 503: Service Unavailable'),
  ])("retries recognized availability failures (%#)", async error => {
    const { updater, controller } = fixture();
    updater.checkForUpdates.mockRejectedValueOnce(error);
    await controller.command("check");
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(2);
    expect(controller.snapshot().state).toBe("available");
  });

  it("deduplicates clicks throughout discovery and keeps downloaded files undisturbed", async () => {
    const { updater, controller, select } = fixture();
    let release!: (value: FeedSelection) => void;
    select.mockImplementationOnce(() => new Promise(resolve => { release = resolve; }));
    const checking = controller.command("check");
    expect(controller.snapshot().state).toBe("checking");
    await controller.command("check"); await controller.command("download"); await controller.command("install");
    expect(select).toHaveBeenCalledOnce();
    release({ version: VERSION, primary: githubFeed(TAG), fallback: ossFeed(TAG) });
    await checking;
    await controller.command("check");
    expect(select).toHaveBeenCalledOnce();
    await controller.command("download");
    await controller.command("check"); await controller.command("download");
    expect(select).toHaveBeenCalledOnce();
    expect(updater.downloadUpdate).toHaveBeenCalledOnce();
    expect(controller.snapshot().state).toBe("downloaded");
  });
});
