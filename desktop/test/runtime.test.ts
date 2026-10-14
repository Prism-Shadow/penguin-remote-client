import { describe, expect, it } from "vitest";
import {
  isLocalAppURL,
  readyURLFromOutput,
  remoteBinaryName,
  remoteBinaryPath,
} from "../src/runtime.js";

describe("desktop runtime helpers", () => {
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
