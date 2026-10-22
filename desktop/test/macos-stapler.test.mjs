import { describe, expect, it } from "vitest";
import { runXcrun } from "../scripts/xcrun-with-stapler-retry.cjs";

const networkError = { status: 68, stdout: "", stderr: 'Error Domain=NSURLErrorDomain Code=-1005 "The network connection was lost."' };

function fixture(outcomes) {
  const calls = [], sleeps = [], messages = [];
  const dependencies = {
    run: (...args) => { calls.push(args); return outcomes[calls.length - 1]; },
    sleep: delay => sleeps.push(delay),
    stdout: message => messages.push(message), stderr: message => messages.push(message),
  };
  return { calls, sleeps, messages, dependencies };
}

describe("macOS notarization ticket transport", () => {
  it.each(["staple", "validate"])("retries transient %s failures with the exact command and path", action => {
    const args = ["stapler", action, "-v", "Penguin Remote.app"];
    const test = fixture([networkError, networkError, { status: 0, stdout: "ticket accepted" }]);
    expect(runXcrun(args, test.dependencies)).toBe(0);
    expect(test.sleeps).toEqual([5_000, 15_000]);
    expect(test.calls.map(([command, parameters]) => [command, parameters])).toEqual(Array(3).fill(["/usr/bin/xcrun", args]));
    expect(test.calls[0][2]).toMatchObject({ timeout: 60_000 });
    expect(test.messages).toContain("ticket accepted");
  });

  it("stops after four failed attempts and propagates the failure", () => {
    const test = fixture(Array(4).fill(networkError));
    expect(runXcrun(["stapler", "staple", "app"], test.dependencies)).toBe(68);
    expect(test.calls).toHaveLength(4);
    expect(test.sleeps).toEqual([5_000, 15_000, 30_000]);
  });

  it.each([
    { status: 65, stderr: "Record not found" },
    { status: 68, stderr: "Unsupported bundle or invalid signature" },
    { status: 1, stderr: networkError.stderr },
    { status: null, signal: "SIGTERM", stderr: "" },
    { status: null, error: Object.assign(new Error("not found"), { code: "ENOENT" }) },
  ])("does not retry permanent failures: %j", failure => {
    const test = fixture([failure]);
    expect(runXcrun(["stapler", "validate", "app"], test.dependencies)).not.toBe(0);
    expect(test.calls).toHaveLength(1);
    expect(test.sleeps).toEqual([]);
  });

  it("bounds a hung ticket operation and permits a successful retry", () => {
    const test = fixture([{ status: null, error: Object.assign(new Error("timed out"), { code: "ETIMEDOUT" }) }, { status: 0 }]);
    expect(runXcrun(["stapler", "validate", "app"], test.dependencies)).toBe(0);
    expect(test.sleeps).toEqual([5_000]);
  });

  it.each([
    ["notarytool", "submit", "app.zip", "--wait"],
    ["--find", "notarytool"],
    ["stapler", "unsupported", "app"],
  ])("passes other xcrun commands through once without capturing credentials: %j", (...args) => {
    const test = fixture([{ status: 1 }]);
    expect(runXcrun(args, test.dependencies)).toBe(1);
    expect(test.calls).toEqual([["/usr/bin/xcrun", args, { stdio: "inherit" }]]);
    expect(test.sleeps).toEqual([]);
    expect(test.messages).toEqual([]);
  });
});
