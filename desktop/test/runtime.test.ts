import { describe, expect, it } from "vitest";
import {
  configPathForService,
  isLocalAppURL,
  readyURLFromOutput,
  remoteBinaryName,
  remoteBinaryPath,
} from "../src/runtime.js";

describe("desktop runtime helpers", () => {
  it("isolates production and test state while keeping canonical URLs stable", () => {
    const production = configPathForService("app-data", "https://go.penguin.ooo");
    const staging = configPathForService("app-data", "https://staging.example.com");
    expect(production).not.toBe(staging);
    expect(production).not.toContain("go.penguin.ooo");
    expect(production).toBe(configPathForService("app-data", "https://GO.PENGUIN.OOO:443/"));
    expect(production).not.toBe(configPathForService("app-data", "https://go.penguin.ooo:8443"));
    expect(production).not.toBe(configPathForService("app-data", "https://go.penguin.ooo/test"));
    expect(() => configPathForService("app-data", "https://user:password@go.penguin.ooo")).toThrow();
    expect(() => configPathForService("app-data", "https://go.penguin.ooo?test=1")).toThrow();
    expect(() => configPathForService("app-data", "https://go.penguin.ooo#test")).toThrow();
  });
  it("parses the embedded client's ready URL", () => {
    expect(
      readyURLFromOutput("Penguin Remote client is ready: http://127.0.0.1:43821\n"),
    ).toBe("http://127.0.0.1:43821");
    expect(readyURLFromOutput("starting")).toBeNull();
  });

  it("resolves platform binary names and packaged paths", () => {
    expect(remoteBinaryName("win32")).toBe("penguin-remote.exe");
    expect(remoteBinaryName("darwin")).toBe("penguin-remote");
    expect(remoteBinaryPath("C:/app", "C:/resources", "win32", true)).toContain("resources");
    expect(remoteBinaryPath("C:/app", "C:/resources", "win32", false)).toContain("native");
  });

  it("allows navigation only within the embedded app origin", () => {
    const origin = "http://127.0.0.1:43821";
    expect(isLocalAppURL(origin + "/callback", origin)).toBe(true);
    expect(isLocalAppURL("https://go.penguin.ooo/login", origin)).toBe(false);
    expect(isLocalAppURL("not a url", origin)).toBe(false);
  });
});
