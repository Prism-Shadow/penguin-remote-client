import { readFileSync } from "node:fs";
import { createContext, runInContext } from "node:vm";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

class Element {
  textContent = "";
  innerHTML = "";
  className = "";
  hidden = false;
  disabled = false;
  checked = false;
  value = "";
  dataset: Record<string, string> = {};
  attributes = new Map<string, string>();
  children: Element[] = [];
  parent: Element | undefined;
  selectors = new Map<string, Element>();
  classes = new Set<string>();
  onclick: () => Promise<void> = async () => {};
  onchange: (event: { target: Element }) => Promise<void> = async () => {};
  classList = {
    toggle: (key: string, on: boolean) => on ? this.classes.add(key) : this.classes.delete(key),
    add: (key: string) => this.classes.add(key),
    remove: (key: string) => this.classes.delete(key),
  };
  setAttribute(key: string, value: string) { this.attributes.set(key, value); }
  removeAttribute(key: string) { delete this.dataset[key.replace("data-", "")]; }
  append(element: Element) { element.parent = this; this.children.push(element); }
  remove() { if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this); }
  querySelector(selector: string) {
    if (!this.selectors.has(selector)) this.selectors.set(selector, new Element());
    return this.selectors.get(selector)!;
  }
}

const status = {
  language: "zh", theme: "system", loggedIn: true, runnerStatus: "stopped", connectionStatus: "idle",
  desired: false, harnessFound: true, lastError: "", authStatus: "unchecked",
  harnesses: [{url: "http://localhost:7364", kind: "desktop"}], localUrl: "http://localhost:7364",
  entitlementStatus: "active", expiresAt: "2099-01-01T00:00:00Z",
};

function renderer() {
  const html = readFileSync(new URL("../../cmd/penguin-remote/assets/app.html", import.meta.url), "utf8");
  const elements = new Map([...html.matchAll(/id="([^"]+)"/g)].map(match => [match[1]!, new Element()]));
  const element = (id: string) => elements.get(id)!;
  const root = new Element();
  // Leave the initial background request pending; each test controls subsequent requests.
  const context = createContext({
    document: {documentElement: root, getElementById: element, createElement: () => new Element(), querySelectorAll: () => [], addEventListener: () => {}},
    window: {addEventListener: () => {}}, location: {href: ""},
    fetch: () => new Promise(() => {}), setInterval: () => {}, setTimeout, clearTimeout, URL, console,
  });
  runInContext(html.match(/<script>([\s\S]*?)<\/script>/)![1]!, context);
  const respond = (body: unknown, ok = true) => ({ok, text: async () => typeof body === "string" ? body : JSON.stringify(body)});
  return {
    element, root, context,
    respond,
    response: (body: unknown, ok = true) => { context.fetch = vi.fn(async () => respond(body, ok)); },
    render: (overrides = {}) => runInContext(`render(${JSON.stringify({...status, ...overrides})})`, context),
    messages: () => element("toastRegion").children.map(toast => toast.querySelector(".toast-message").textContent),
    toasts: () => element("toastRegion").children,
  };
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("client action notifications", () => {
  it("shows scan progress, blocks repeated clicks, then reports the discovered count", async () => {
    const ui = renderer();
    let finish!: (value: unknown) => void;
    const fetch = vi.fn(() => new Promise(resolve => { finish = resolve; }));
    ui.context.fetch = fetch;
    const pending = ui.element("rescan").onclick();
    expect(ui.messages()).toEqual(["正在扫描本机 Harness…"]);
    expect(ui.element("rescan").disabled).toBe(true);
    await ui.element("rescan").onclick();
    expect(fetch).toHaveBeenCalledTimes(1);
    finish(ui.respond(status));
    await pending;
    expect(ui.messages()).toEqual(["扫描完成，发现 1 个本机 Harness"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("success");
    expect(ui.element("rescan").disabled).toBe(false);
    vi.advanceTimersByTime(2999);
    expect(ui.messages()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(ui.messages()).toEqual([]);
  });

  it("treats a scan with no Harness as a warning even when the API returns 200", async () => {
    const ui = renderer();
    ui.response({...status, harnessFound: false, harnesses: [], lastError: "service_unavailable"});
    await ui.element("rescan").onclick();
    expect(ui.messages()).toEqual(["未发现本机 Harness，请先启动后重新扫描"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("warning");
  });

  it("keeps an action error visible through status polling until it expires automatically", async () => {
    const ui = renderer();
    ui.response("service_unavailable\n", false);
    await ui.element("rescan").onclick();
    expect(ui.toasts()[0]!.dataset.kind).toBe("error");
    const message = ui.messages()[0];
    expect(message).toMatch(/^扫描失败：/);
    expect(message).toContain("暂时无法连接");
    ui.render();
    expect(ui.messages()).toEqual([message]);
    expect(ui.element("lastError").classes.has("visible")).toBe(false);
    vi.advanceTimersByTime(4999);
    expect(ui.messages()).toEqual([message]);
    vi.advanceTimersByTime(1);
    expect(ui.messages()).toEqual([]);
    expect(ui.element("rescan").disabled).toBe(false);
  });

  it("keeps automatic subscription refresh silent and preserves initial language and theme", async () => {
    const ui = renderer();
    ui.response({...status, language: "en", theme: "dark"});
    await runInContext("refreshSubscription()", ui.context);
    expect(ui.messages()).toEqual([]);
    expect(ui.root.dataset.theme).toBe("dark");
    await ui.element("refreshSubscription").onclick();
    expect(ui.messages()[0]).toContain("Subscription:");
    ui.response({...status, entitlementStatus: "unknown"});
    await ui.element("refreshSubscription").onclick();
    expect(ui.toasts()[1]!.dataset.kind).toBe("warning");
  });

  it("waits for actual connectivity instead of reporting success on start acknowledgement", async () => {
    const ui = renderer();
    ui.render();
    ui.context.fetch = vi.fn(async (path: string) => ui.respond(path === "/api/status"
      ? {...status, desired: true, runnerStatus: "starting", connectionStatus: "confirming"}
      : {status: "confirming"}));
    const toggle = ui.element("remoteToggle");
    toggle.checked = true;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual(["正在开启远程访问…"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("loading");
    vi.advanceTimersByTime(10000);
    expect(ui.messages()).toHaveLength(1);
    ui.render({desired: true, runnerStatus: "running", connectionStatus: "connected"});
    expect(ui.messages()).toEqual(["远程访问已连接"]);
    ui.render({desired: true, runnerStatus: "running", connectionStatus: "connected"});
    vi.advanceTimersByTime(3000);
    expect(ui.messages()).toEqual([]);
  });

  it("reports stop errors without a false success and does not confuse a stale connected state with stopping", async () => {
    const ui = renderer();
    ui.render({desired: true, runnerStatus: "running", connectionStatus: "connected"});
    ui.response({...status, desired: true, runnerStatus: "running", connectionStatus: "connected"});
    const toggle = ui.element("remoteToggle");
    toggle.checked = false;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual(["正在停止远程访问…"]);
    ui.render({lastError: "save_failed"});
    expect(ui.toasts()[0]!.dataset.kind).toBe("error");
    expect(ui.messages()[0]).toMatch(/^停止远程访问失败：/);
    expect(ui.messages()[0]).not.toBe("远程访问已停止");
  });

  it("replaces an unfinished start notification when the user stops connecting", async () => {
    const ui = renderer();
    ui.response({...status, desired: true, runnerStatus: "starting", connectionStatus: "confirming"});
    const toggle = ui.element("remoteToggle");
    toggle.checked = true;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual(["正在开启远程访问…"]);
    ui.response(status);
    toggle.checked = false;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual(["远程访问已停止"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("success");
    vi.advanceTimersByTime(3000);
    expect(ui.messages()).toEqual([]);
  });

  it.each(["zh", "en"])("reports a successful stop while server cleanup is pending in %s", async language => {
    const ui = renderer();
    ui.render({desired: true, runnerStatus: "running", connectionStatus: "connected"});
    ui.response({...status, language, cancelPending: true, lastError: "cancel_pending"});
    const toggle = ui.element("remoteToggle");
    toggle.checked = false;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual([language === "zh" ? "远程访问已停止" : "Remote access stopped"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("success");
    expect(ui.element("lastError").classes.has("info")).toBe(true);
    expect(ui.element("lastError").classes.has("visible")).toBe(true);
    expect(toggle.checked).toBe(false);
    vi.advanceTimersByTime(3000);
    expect(ui.messages()).toEqual([]);
  });

  it("continues waiting for a queued start while the previous connection is being cleaned up", async () => {
    const ui = renderer();
    ui.response({...status, desired: true, runnerStatus: "confirming", connectionStatus: "confirming", cancelPending: true, lastError: "cancel_pending"});
    const toggle = ui.element("remoteToggle");
    toggle.checked = true;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual(["正在开启远程访问…"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("loading");
    ui.render({desired: true, runnerStatus: "running", connectionStatus: "connected"});
    expect(ui.messages()).toEqual(["远程访问已连接"]);
  });

  it("acknowledges cleanup requests without claiming cleanup has completed", async () => {
    const ui = renderer();
    ui.context.fetch = vi.fn(async (path: string) => ui.respond(path === "/api/status" ? {...status, cancelPending: true} : {ok: true}));
    await ui.element("retryCancellation").onclick();
    expect(ui.messages()).toEqual(["正在断开上次连接，请稍候…"]);
    expect(ui.toasts()[0]!.dataset.kind).toBe("info");
  });

  it("provides browser sign-in instructions after requesting the login URL", async () => {
    const ui = renderer();
    ui.response({loginUrl: "https://login.example.test/remote"});
    await ui.element("login").onclick();
    expect(ui.context.location.href).toBe("https://login.example.test/remote");
    expect(ui.messages()).toEqual(["请在浏览器中完成 Penguin Go 登录"]);
    expect(ui.element("login").disabled).toBe(false);
  });

  it("reports successful stopping and a rejected start accurately", async () => {
    const ui = renderer();
    const toggle = ui.element("remoteToggle");
    ui.response(status);
    toggle.checked = false;
    await toggle.onchange({target: toggle});
    expect(ui.messages()).toEqual(["远程访问已停止"]);
    ui.context.fetch = vi.fn(async (path: string) => path === "/api/status"
      ? ui.respond(status)
      : ui.respond("save_failed\n", false));
    toggle.checked = true;
    await toggle.onchange({target: toggle});
    expect(ui.toasts()[1]!.dataset.kind).toBe("error");
    expect(ui.messages()[1]).toContain("无法保存设置");
    expect(ui.messages()[1]).toMatch(/^开启远程访问失败：/);
    expect(toggle.checked).toBe(false);
    expect(toggle.disabled).toBe(false);
  });

  it.each([
    ["rescan", "扫描失败：请稍后重试。"],
    ["login", "无法打开登录页面：请稍后重试。"],
    ["refreshSubscription", "刷新订阅失败：请稍后重试。"],
    ["retryCancellation", "断开上次连接失败：请稍后重试。"],
    ["langEn", "切换语言失败：请稍后重试。"],
    ["themeDark", "切换主题失败：请稍后重试。"],
  ])("identifies which action failed for %s", async (button, message) => {
    const ui = renderer();
    ui.response("unexpected_error\n", false);
    await ui.element(button!).onclick();
    expect(ui.messages()).toEqual([message]);
  });

  it("replaces a pending toast's timer and bounds the stack", () => {
    const ui = renderer();
    runInContext("notify('remoteStoppedToast', 'success', {}, 99)", ui.context);
    vi.advanceTimersByTime(2000);
    runInContext("notify('scanningToast', 'loading', {}, 99)", ui.context);
    vi.advanceTimersByTime(5000);
    expect(ui.messages()).toEqual(["正在扫描本机 Harness…"]);
    runInContext("notify('remoteStoppedToast'); notify('remoteStoppedToast'); notify('remoteStoppedToast');", ui.context);
    expect(ui.messages()).toHaveLength(3);
  });
});
