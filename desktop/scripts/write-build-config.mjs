import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const desktopDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const output = path.join(desktopDir, "dist", "build-config.json");
const penguinGoUrl = (process.env.PENGUIN_REMOTE_GO_URL || "https://go.penguin.ooo").trim();

const parsed = new URL(penguinGoUrl);
if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
  throw new Error("PENGUIN_REMOTE_GO_URL must use http or https");
}

fs.mkdirSync(path.dirname(output), { recursive: true });
fs.writeFileSync(output, JSON.stringify({ penguinGoUrl }, null, 2) + "\n", "utf8");
console.log("Desktop Penguin Go URL: " + penguinGoUrl);
