#!/usr/bin/env node
"use strict";

const { spawnSync } = require("node:child_process");

const delays = [5_000, 15_000, 30_000];
const wait = milliseconds => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, milliseconds);

// Installed as xcrun only on ephemeral signed macOS runners. Using the absolute
// system executable avoids recursion and preserves every other xcrun command.
function runXcrun(args, dependencies = {}) {
  const run = dependencies.run ?? spawnSync;
  const sleep = dependencies.sleep ?? wait;
  const stdout = dependencies.stdout ?? (text => process.stdout.write(text));
  const stderr = dependencies.stderr ?? (text => process.stderr.write(text));
  const isTicketOperation = args[0] === "stapler" && ["staple", "validate"].includes(args[1]);
  if (!isTicketOperation) {
    const result = run("/usr/bin/xcrun", args, { stdio: "inherit" });
    if (result.error) stderr(result.error.message + "\n");
    return result.status ?? 1;
  }

  for (let attempt = 0; ; attempt++) {
    const result = run("/usr/bin/xcrun", args, {
      encoding: "utf8", stdio: ["inherit", "pipe", "pipe"], timeout: 60_000,
      maxBuffer: 1024 * 1024,
    });
    const output = (result.stdout ?? "") + (result.stderr ?? "");
    if (result.stdout) stdout(result.stdout);
    if (result.stderr) stderr(result.stderr);
    if (result.error) stderr(result.error.message + "\n");
    if (!result.error && !result.signal && result.status === 0) return 0;

    // Error 68 alone also covers permanent failures. Require a transport error
    // from URLSession, or an actual command timeout, before retrying a ticket.
    const networkFailure = result.error?.code === "ETIMEDOUT" ||
      (result.status === 68 && /NSURLErrorDomain\s+Code=-(?:1001|1003|1004|1005|1006|1009)\b/.test(output));
    if (!networkFailure || attempt >= delays.length) return result.status || 1;
    stderr(`[stapler] Network failure; retry ${attempt + 1}/${delays.length} in ${delays[attempt] / 1000}s.\n`);
    sleep(delays[attempt]);
  }
}

module.exports = { runXcrun };
if (require.main === module) process.exitCode = runXcrun(process.argv.slice(2));
