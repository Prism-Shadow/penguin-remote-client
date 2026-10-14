import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { appendDesktopLog, MAX_LOG_BYTES } from "../src/logging.js";

const directories: string[] = [];

function logPath(): string {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "remote-log-test-"));
  directories.push(directory);
  return path.join(directory, "penguin-remote.log");
}

afterEach(() => {
  for (const directory of directories.splice(0)) {
    if (path.dirname(path.resolve(directory)) !== path.resolve(os.tmpdir())) throw new Error("Unexpected test directory");
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

describe("desktop diagnostic logs", () => {
  it("preserves UTF-8 output with timestamps", () => {
    const file = logPath();
    appendDesktopLog(file, "连接诊断");
    expect(fs.readFileSync(file, "utf8")).toMatch(/^\[.+Z\] 连接诊断\n$/);
  });

  it("rotates a full log and retains only one backup", () => {
    const file = logPath();
    fs.writeFileSync(file, "a".repeat(MAX_LOG_BYTES));
    fs.writeFileSync(file + ".1", "obsolete");
    appendDesktopLog(file, "first new event");
    expect(fs.statSync(file + ".1").size).toBe(MAX_LOG_BYTES);
    expect(fs.readFileSync(file, "utf8")).toContain("first new event");
    fs.writeFileSync(file, "b".repeat(MAX_LOG_BYTES));
    appendDesktopLog(file, "second new event");
    expect(fs.readFileSync(file + ".1", "utf8")).toBe("b".repeat(MAX_LOG_BYTES));
    expect(fs.readdirSync(path.dirname(file))).toHaveLength(2);
  });
});
