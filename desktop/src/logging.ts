import fs from "node:fs";

export const MAX_LOG_BYTES = 2 * 1024 * 1024;

export function appendDesktopLog(filePath: string, message: string): void {
  const line = "[" + new Date().toISOString() + "] " + message + "\n";
  if (fs.existsSync(filePath) && fs.statSync(filePath).size + Buffer.byteLength(line, "utf8") > MAX_LOG_BYTES) {
    const backup = filePath + ".1";
    if (fs.existsSync(backup)) fs.unlinkSync(backup);
    fs.renameSync(filePath, backup);
  }
  fs.appendFileSync(filePath, line, "utf8");
}
