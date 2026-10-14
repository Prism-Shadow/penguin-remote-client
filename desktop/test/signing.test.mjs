import { afterEach, describe, expect, it, vi } from "vitest";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import integrity from "../build/native-integrity.cjs";
import evsign from "../build/evsign.cjs";

afterEach(() => vi.unstubAllEnvs());

describe("native integrity across signing", () => {
  it("requires an exact copy before signing and detects subsequent source or package changes", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-native-signing-test-"));
    const source = path.join(dir, "source");
    const packaged = path.join(dir, "packaged");
    const receipt = path.join(dir, "receipt.json");
    try {
      fs.writeFileSync(source, "native-build");
      fs.writeFileSync(packaged, "wrong-native-build");
      expect(() => integrity.recordCopiedNative(source, packaged, receipt)).toThrow(/before signing/);
      expect(fs.existsSync(receipt)).toBe(false);
      fs.copyFileSync(source, packaged);
      integrity.recordCopiedNative(source, packaged, receipt);
      expect(() => integrity.verifyNativeIntegrity(source, packaged, receipt)).not.toThrow();
      fs.appendFileSync(packaged, "signature");
      expect(() => integrity.verifyNativeIntegrity(source, packaged, receipt)).toThrow();
      integrity.recordSignedNative(source, packaged, receipt);
      expect(() => integrity.verifyNativeIntegrity(source, packaged, receipt)).not.toThrow();
      fs.appendFileSync(source, "replaced-source");
      expect(() => integrity.recordSignedNative(source, packaged, receipt)).toThrow(/source changed/);
      expect(() => integrity.verifyNativeIntegrity(source, packaged, receipt)).toThrow();
      fs.writeFileSync(source, "native-build");
      fs.appendFileSync(packaged, "modified-after-signing");
      expect(() => integrity.verifyNativeIntegrity(source, packaged, receipt)).toThrow();
      fs.unlinkSync(receipt);
      expect(() => integrity.verifyNativeIntegrity(source, packaged, receipt)).toThrow();
    } finally {
      if (!path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep + "remote-native-signing-test-")) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
});

describe("EVSign release gate", () => {
  it("fails credential preparation before any signing-service request when required secrets are absent", () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "remote-signing-credentials-test-"));
    try {
      const windows = process.platform === "win32";
      const script = fileURLToPath(new URL(windows ? "../scripts/prepare-windows-signing.ps1" :
        "../scripts/prepare-macos-signing.sh", import.meta.url));
      const result = spawnSync(windows ? "powershell.exe" : "bash",
        windows ? ["-NoProfile", "-NonInteractive", "-File", script] : [script], {
          encoding: "utf8", timeout: 15_000,
          env: { ...process.env, REQUIRE_WINDOWS_SIGNING: "true", REQUIRE_MACOS_SIGNING: "true",
            EVSIGN_KEY: "", CSC_LINK: "", CSC_KEY_PASSWORD: "", APPLE_API_KEY_BASE64: "",
            APPLE_API_KEY_ID: "", APPLE_API_ISSUER: "", GITHUB_ENV: path.join(dir, "github-env") },
        });
      expect(result.error).toBeUndefined();
      expect(result.status).not.toBe(0);
      expect(result.stdout + result.stderr).toContain(windows ? "Missing EVSIGN_KEY" : "Missing macOS signing secrets");
    } finally {
      if (!path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep + "remote-signing-credentials-test-")) throw new Error("Unsafe test cleanup path");
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
  it("rejects missing credentials for every Remote executable including NSIS uninstallers", async () => {
    vi.stubEnv("EVSIGN_REQUIRE", "true");
    vi.stubEnv("EVSIGN_KEY", "");
    for (const name of ["Penguin Remote.exe", "penguin-remote.exe", "elevate.exe",
      "penguin-remote-win-x64.exe", "temporary.__uninstaller.exe"]) {
      await expect(evsign({ path: name, hash: "sha256" })).rejects.toThrow(/EVSIGN_KEY is required/);
    }
  });
  it("allows an explicit unsigned dry-run and rejects a missing CLI when credentials are supplied", async () => {
    vi.stubEnv("EVSIGN_REQUIRE", "false");
    vi.stubEnv("EVSIGN_KEY", "");
    await expect(evsign({ path: "Penguin Remote.exe", hash: "sha256" })).resolves.toBeUndefined();
    vi.stubEnv("EVSIGN_KEY", "test-key");
    vi.stubEnv("EVSIGN_CLIENT", path.join(os.tmpdir(), "remote-nonexistent-cli", "evsign-client.exe"));
    await expect(evsign({ path: "Penguin Remote.exe", hash: "sha256" })).rejects.toThrow(/client was not found/);
  });
});
