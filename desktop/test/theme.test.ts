import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { configPathForService } from "../src/runtime.js";
import { readTheme, themeBackgroundColor } from "../src/theme.js";

const directories: string[] = [];

function themeFile() {
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), "remote-theme-test-"));
  directories.push(userData);
  const configPath = configPathForService(userData, "https://go.example.test");
  fs.mkdirSync(path.dirname(configPath), { recursive: true });
  return { configPath, preference: path.join(path.dirname(configPath), "ui-theme") };
}

afterEach(() => {
  for (const directory of directories.splice(0)) fs.rmSync(directory, { recursive: true, force: true });
});

describe("initial window theme", () => {
  it("reads the embedded client's service preference again whenever a window opens", () => {
    const { configPath, preference } = themeFile();
    expect(readTheme(configPath)).toBe("system");
    for (const theme of ["dark", "light", "system"] as const) {
      fs.writeFileSync(preference, theme + "\n");
      expect(readTheme(configPath)).toBe(theme);
    }
  });

  it("uses the system theme for missing or invalid preferences", () => {
    const { configPath, preference } = themeFile();
    expect(readTheme(configPath)).toBe("system");
    for (const value of ["", "other", '{"theme":"dark"}']) {
      fs.writeFileSync(preference, value);
      expect(readTheme(configPath)).toBe("system");
    }
    fs.writeFileSync(preference, "  dark\r\n");
    expect(readTheme(configPath)).toBe("dark");
  });

  it.each([false, true])("matches the first page background with system dark = %s", systemDark => {
    expect(themeBackgroundColor("dark", systemDark)).toBe("#000000");
    expect(themeBackgroundColor("light", systemDark)).toBe("#ffffff");
    expect(themeBackgroundColor("system", systemDark)).toBe(systemDark ? "#000000" : "#ffffff");
  });
});
