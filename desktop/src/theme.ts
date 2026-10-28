import fs from "node:fs";
import path from "node:path";

export type ThemeMode = "system" | "light" | "dark";

export function readTheme(configPath: string): ThemeMode {
  try {
    const value = fs.readFileSync(path.join(path.dirname(configPath), "ui-theme"), "utf8").trim();
    if (value === "light" || value === "dark") return value;
  } catch {
    // Match the embedded client's default when no saved preference is available.
  }
  return "system";
}

export function themeBackgroundColor(theme: ThemeMode, systemDark: boolean): string {
  return theme === "dark" || (theme === "system" && systemDark) ? "#000000" : "#ffffff";
}
