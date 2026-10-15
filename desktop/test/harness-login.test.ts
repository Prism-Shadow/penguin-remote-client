import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";
import { describe, expect, it } from "vitest";

// Exercise the embedded renderer used by Electron, including status priority.
function renderer() {
  const html = readFileSync(new URL("../../cmd/penguin-remote/assets/app.html", import.meta.url), "utf8");
  const elements = new Map([...html.matchAll(/id="([^"]+)"/g)].map(match => {
    const classes = new Set<string>();
    return [match[1]!, {
      textContent: "", innerHTML: "", hidden: false, disabled: false, checked: false, value: "",
      classList: { toggle: (key: string, on: boolean) => on ? classes.add(key) : classes.delete(key) },
      setAttribute: () => {}, classes,
    }] as const;
  }));
  const document = {
    documentElement: { dataset: {}, removeAttribute: () => {} },
    getElementById: (id: string) => {
      const element = elements.get(id);
      if (!element) throw new Error(`Missing renderer element ${id}`);
      return element;
    },
    querySelectorAll: () => [], addEventListener: () => {},
  };
  const context = createContext({ document, window: { addEventListener: () => {} },
    fetch: () => new Promise(() => {}), setInterval: () => {}, URL, console });
  const script = html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
  if (!script) throw new Error("Missing embedded app script");
  runInContext(script, context);
  const state = {
    loggedIn: true, runnerStatus: "running", connectionStatus: "connected", authStatus: "ready",
    desired: true, harnessFound: true, harnessLoginMode: "manual", lastError: "",
    entitlementStatus: "active", expiresAt: "2099-01-01T00:00:00Z", publicUrl: "https://public.remote.test",
  };
  return {
    render: (overrides = {}, language = "zh") => runInContext(`setLanguage(${JSON.stringify(language)}); render(${JSON.stringify({ ...state, ...overrides })});`, context),
    element: document.getElementById,
  };
}

describe("Harness login compatibility status", () => {
  it.each(["zh", "en"])("shows manual sign-in as a healthy connection in %s", language => {
    const ui = renderer();
    ui.render({}, language);
    expect(ui.element("runnerMetric").textContent).toBe(language === "zh" ? "已连接" : "Connected");
    expect(ui.element("authStatus").textContent).toBe(language === "zh" ? "本机连接已就绪" : "Local connection is ready");
    expect(ui.element("authDiagnostic").textContent).toBe(language === "zh" ? "本机连接已就绪" : "Local connection is ready");
    expect(ui.element("lastError").classes.has("info")).toBe(false);
    expect(ui.element("lastError").classes.has("visible")).toBe(false);
    expect(ui.element("lastError").textContent).toBe("");
    expect(ui.element("remoteToggle").checked).toBe(true);
    expect(ui.element("remoteToggle").disabled).toBe(false);
  });

  it("keeps automatic sign-in and reconnection status distinct", () => {
    const ui = renderer();
    ui.render({ harnessLoginMode: "automatic" });
    expect(ui.element("authStatus").textContent).toBe("自动登录可用");
    expect(ui.element("lastError").classes.has("visible")).toBe(false);
    ui.render({ connectionStatus: "reconnecting" });
    expect(ui.element("lastError").textContent).toContain("正在自动重连");
    expect(ui.element("lastError").textContent).not.toContain("连接正常");
    expect(ui.element("lastError").textContent).not.toContain("无需重新登录");
  });

  it("prioritizes explicit failures and clears the mode after stopping", () => {
    const ui = renderer();
    ui.render({ lastError: "harness_authorization_failed" });
    expect(ui.element("lastError").classes.has("info")).toBe(false);
    expect(ui.element("lastError").textContent).toContain("请重启 Harness 并重新扫描后重试");
    expect(ui.element("lastError").textContent).not.toContain("api-token");
    ui.render({ desired: false, runnerStatus: "stopped", connectionStatus: "idle", authStatus: "unchecked", harnessLoginMode: "" });
    expect(ui.element("authStatus").textContent).toBe("开启时验证本机连接");
    expect(ui.element("lastError").classes.has("visible")).toBe(false);
  });

  it.each(["zh", "en"])("shows unavailable Harness as a waiting notice in %s", language => {
    const ui = renderer();
    ui.render({ runnerStatus: "reconnecting", connectionStatus: "reconnecting", harnessFound: false,
      authStatus: "unchecked", harnessLoginMode: "", lastError: "harness_unavailable" }, language);
    expect(ui.element("lastError").textContent).toBe(language === "zh"
      ? "尚未连接到本机 Harness。请先启动 Harness，Remote 会自动重试。"
      : "Local Harness is not connected yet. Start Harness; Remote will retry automatically.");
    expect(ui.element("lastError").classes.has("info")).toBe(true);
    expect(ui.element("remoteToggle").checked).toBe(true);
    expect(ui.element("remoteToggle").disabled).toBe(false);
  });

  it.each(["zh", "en"])("keeps access and public sign-in failures actionable in %s", language => {
    const ui = renderer();
    for (const code of ["harness_authorization_failed", "harness_session_failed"]) {
      ui.render({ lastError: code }, language);
      const message = ui.element("lastError").textContent;
      expect(message).toContain(language === "zh" ? "请重启 Harness" : "Restart Harness");
      expect(message).not.toMatch(/api-token|不会自动切换|will not switch/);
      expect(ui.element("lastError").classes.has("info")).toBe(false);
    }
    ui.render({ connectionStatus: "reconnecting" }, language);
    expect(ui.element("lastError").textContent).not.toMatch(/无需重新登录|do not need to sign in again/);
  });
});
