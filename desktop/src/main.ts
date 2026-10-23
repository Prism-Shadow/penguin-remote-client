import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import type { ChildProcess } from "node:child_process";
import { app, BrowserWindow, dialog, shell, session, powerMonitor, net } from "electron";
import electronUpdater from "electron-updater";
import { UpdateController, updateUnsupportedReason } from "./update-controller.js";
import { startUpdateBridge } from "./update-bridge.js";
import { WindowsUpdater } from "./windows-updater.js";
import { selectUpdateFeed } from "./update-source.js";
import {
  configPathForService,
  isLocalAppURL,
  readyURLFromOutput,
  remoteBinaryPath,
  type DesktopBuildConfig,
} from "./runtime.js";

import {startSystemBridge} from "./network.js";
import { appendDesktopLog } from "./logging.js";
import { createNetworkDiagnostics } from "./network-diagnostics.js";
 let networkBridge: Awaited<ReturnType<typeof startSystemBridge>> | null = null;
 const APP_ID = "com.prismshadow.penguinremote";
const DEFAULT_PENGUIN_GO_URL = "https://go.penguin.ooo";
const START_TIMEOUT_MS = 30_000;

app.setName("Penguin Remote");
if (process.platform === "win32") app.setAppUserModelId(APP_ID);

let mainWindow: BrowserWindow | null = null;
let remoteProcess: ChildProcess | null = null;
let appOrigin: string | null = null;
let quitting = false;
let updateBridge: Awaited<ReturnType<typeof startUpdateBridge>> | null = null;
const autoUpdater = process.platform === "win32" ? new WindowsUpdater() : electronUpdater.autoUpdater;
autoUpdater.logger = null;
const updates = new UpdateController(autoUpdater, app.getVersion(), updateUnsupportedReason(
  app.isPackaged, app.getVersion(), process.platform, process.arch, process.env.APPIMAGE,
), async () => {
  // Keep the existing before-quit cleanup. On macOS the updater first prepares
  // the native installation; a preparation error must leave the client running.
  autoUpdater.quitAndInstall(false, true);
}, message => appendLog("[updater] " + message), () => selectUpdateFeed({
  fetchFn: (url, init) => net.fetch(url, init),
  log: message => appendLog("[updater] " + message),
}));

function appendLog(message: string): void {
  try {
    const userData = app.getPath("userData");
    fs.mkdirSync(userData, { recursive: true });
    appendDesktopLog(
      path.join(userData, "penguin-remote.log"),
      message,
    );
  } catch {
    // Logging must never prevent the desktop shell from starting or stopping.
  }
}

function buildConfig(): DesktopBuildConfig {
  const configPath = path.join(app.getAppPath(), "dist", "build-config.json");
  try {
    const value = JSON.parse(fs.readFileSync(configPath, "utf8")) as Partial<DesktopBuildConfig>;
    if (typeof value.penguinGoUrl === "string" && value.penguinGoUrl.trim()) {
      return { penguinGoUrl: value.penguinGoUrl.trim() };
    }
  } catch (error) {
    appendLog("Could not read build config: " + String(error));
  }
  return { penguinGoUrl: DEFAULT_PENGUIN_GO_URL };
}

async function startRemoteProcess(): Promise<string> {
  const executable = remoteBinaryPath(
    app.getAppPath(),
    process.resourcesPath,
    process.platform,
    app.isPackaged,
  );
  if (!fs.existsSync(executable)) throw new Error("Bundled client not found: " + executable);

  const userData = app.getPath("userData");
  const penguinGoURL = buildConfig().penguinGoUrl;
  const configPath = configPathForService(userData, penguinGoURL);
  fs.mkdirSync(path.dirname(configPath), { recursive: true });
  networkBridge = await startSystemBridge(
    url => session.defaultSession.resolveProxy(url),
    process.env.PENGUIN_REMOTE_DIAGNOSTICS === "1" ? createNetworkDiagnostics(path.join(userData, "network-diagnostics.log")) : undefined,
  );
 const args = [
    "app",
 "-system-proxy",networkBridge.url,
    "-listen",
    "127.0.0.1:0",
    "-config",
    configPath,
    "-penguin-go-url",
    penguinGoURL,
  ];
  appendLog("Starting bundled client from " + executable);

  const child = spawn(executable, args, {
    cwd: userData,
 env:{...process.env,PENGUIN_REMOTE_SYSTEM_PROXY_SECRET:networkBridge.secret,
   PENGUIN_REMOTE_UPDATE_BRIDGE:updateBridge?.url ?? "",PENGUIN_REMOTE_UPDATE_SECRET:updateBridge?.secret ?? ""},
    windowsHide: true,
    stdio: ["ignore", "pipe", "pipe"],
  });
  remoteProcess = child;

  return new Promise<string>((resolve, reject) => {
    let settled = false;
    let stdout = "";
    let stderr = "";
    const timeout = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill();
      reject(new Error("Timed out starting Penguin Remote.\n\n" + stderr.trim()));
    }, START_TIMEOUT_MS);

    child.stdout.on("data", (chunk: Buffer) => {
      const text = chunk.toString("utf8");
      if (!settled) stdout = (stdout + text).slice(-16_384);
      appendLog(text.trimEnd());
      const readyURL = readyURLFromOutput(stdout);
      if (!settled && readyURL !== null) {
        settled = true;
        clearTimeout(timeout);
        resolve(readyURL);
      }
    });
    child.stderr.on("data", (chunk: Buffer) => {
      const text = chunk.toString("utf8");
      if (!settled) stderr = (stderr + text).slice(-16_384);
      appendLog(text.trimEnd());
    });
    child.once("error", (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      reject(error);
    });
    child.once("exit", (code, signal) => {
      remoteProcess = null;
      const detail =
        "Bundled client exited (code=" + String(code) + ", signal=" + String(signal) + ").";
      appendLog(detail);
      if (!settled) {
        settled = true;
        clearTimeout(timeout);
        reject(new Error(detail + "\n\n" + stderr.trim()));
        return;
      }
      if (!quitting) {
        dialog.showErrorBox("Penguin Remote", detail);
        app.quit();
      }
    });
  });
}

function openExternal(target: string): void {
  void shell
    .openExternal(target)
    .catch((error) => appendLog("Open external URL failed"));
}

function createWindow(origin: string): void {
 powerMonitor.on("resume",()=>{void fetch(origin+"/api/wake",{method:"POST"}).catch(()=>{});});
  // Small ICO frames improve the Windows title bar; larger frames retain H.
  const iconName = process.platform === "win32" ? "window-icon.ico" : "icon.png";
  const iconPath = app.isPackaged
    ? path.join(process.resourcesPath, iconName)
    : path.join(app.getAppPath(), "build", iconName);
  mainWindow = new BrowserWindow({
    width: 920,
    height: 680,
    minWidth: 640,
    minHeight: 520,
    show: false,
    autoHideMenuBar: true,
    backgroundColor: "#0d0d0d",
    ...(process.platform !== "darwin" && fs.existsSync(iconPath) ? { icon: iconPath } : {}),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    openExternal(url);
    return { action: "deny" };
  });
  mainWindow.webContents.on("will-navigate", (event, target) => {
    if (isLocalAppURL(target, origin)) return;
    event.preventDefault();
    openExternal(target);
  });
  mainWindow.once("ready-to-show", () => mainWindow?.show());
  mainWindow.on("closed", () => {
    mainWindow = null;
  });
  void mainWindow.loadURL(origin);
}

async function stopRemoteProcess(): Promise<void> {
 networkBridge?.close();networkBridge=null;
  const child = remoteProcess;
  if (child === null) return;
  await new Promise<void>((resolve) => {
    const timeout = setTimeout(resolve, 2_000);
    child.once("exit", () => {
      clearTimeout(timeout);
      resolve();
    });
    child.kill();
  });
}

const hasSingleInstanceLock = app.requestSingleInstanceLock();
if (!hasSingleInstanceLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (mainWindow === null && appOrigin !== null) createWindow(appOrigin);
    if (mainWindow?.isMinimized()) mainWindow.restore();
    mainWindow?.show();
    mainWindow?.focus();
  });

  app.whenReady()
    .then(async () => {
      updateBridge = await startUpdateBridge(updates);
      appOrigin = await startRemoteProcess();
      createWindow(appOrigin);
      updates.start();
      app.on("activate", () => {
        if (mainWindow === null && appOrigin !== null) createWindow(appOrigin);
      });
    })
    .catch((error: unknown) => {
      const detail = error instanceof Error ? (error.stack ?? error.message) : String(error);
      dialog.showErrorBox("Penguin Remote", detail);
      app.exit(1);
    });
}

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", (event) => {
  if (quitting || remoteProcess === null) return;
  event.preventDefault();
  quitting = true;
  void stopRemoteProcess().finally(() => app.quit());
});

app.on("will-quit", () => { updateBridge?.close(); updateBridge = null; });
