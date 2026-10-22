import http from "node:http";
import crypto from "node:crypto";
import type { UpdateController } from "./update-controller.js";

/** Private parent/child bridge. Never expose its credential to the browser page. */
export async function startUpdateBridge(controller: UpdateController): Promise<{ url: string; secret: string; close(): void }> {
  const secret = crypto.randomBytes(32).toString("hex");
  const expected = Buffer.from("Bearer " + secret);
  const server = http.createServer((req, res) => {
    res.setHeader("Cache-Control", "no-store");
    const received = Buffer.from(req.headers.authorization ?? "");
    if (received.length !== expected.length || !crypto.timingSafeEqual(received, expected) || req.headers.origin) {
      res.writeHead(403).end(); return;
    }
    if (req.url === "/status" && req.method === "GET") {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(controller.snapshot())); return;
    }
    const action = req.url?.slice(1);
    if (req.method !== "POST" || (action !== "check" && action !== "download" && action !== "install")) {
      res.writeHead(404).end(); return;
    }
    // Acknowledge before an installer closes the serving process. Completion is polled.
    res.writeHead(202).end();
    void controller.command(action);
  });
  server.requestTimeout = 5_000;
  server.headersTimeout = 5_000;
  await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("Update bridge did not bind");
  return { url: "http://127.0.0.1:" + address.port, secret, close() { controller.close(); server.closeAllConnections(); server.close(); } };
}
