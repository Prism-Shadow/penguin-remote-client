import { describe, expect, it } from "vitest";
import { networkDiagnosticSink, networkReason } from "../src/network-diagnostics.js";
import type { NetworkDiagnostic } from "../src/network-diagnostics.js";
import net from "node:net";
import { startSystemBridge } from "../src/network.js";

const tick = () => new Promise<void>(resolve => setImmediate(resolve));

describe("safe network diagnostics", () => {
  it("keeps output bounded and does not wait for a blocked destination", async () => {
    let release!: () => void;
    const blocked = new Promise<void>(resolve => { release = resolve; });
    const lines: string[] = [];
    let first = true;
    const record = networkDiagnosticSink(async line => {
      if (first) { first = false; await blocked; }
      lines.push(line);
    });
    record({ event: "bridge_connected", connection: 1 });
    for (let i = 0; i < 140; i++) record({ event: "bridge_closed", connection: i + 2, reason: "upstream_closed" });
    expect(lines).toHaveLength(0);
    release();
    await tick();
    const events = lines.map(line => JSON.parse(line));
    expect(events).toHaveLength(130);
    expect(events.find(event => event.event === "diagnostic_events_dropped").droppedEvents).toBe(12);
  });

  it("drops credentials, raw errors, routes and additional properties", async () => {
    const lines: string[] = [];
    const record = networkDiagnosticSink(async line => { lines.push(line); });
    record({ event: "bridge_closed", connection: 1, reason: "secret-token" });
    record({ event: "bridge_closed", connection: 1, reason: "connection_reset", kind: "secret-proxy", ...{ cookie: "secret-cookie" } });
    await tick();
    expect(lines).toHaveLength(1);
    expect(lines[0]).not.toContain("secret");
    expect(networkReason(Object.assign(new Error("secret URL"), { code: "ECONNRESET" }))).toBe("connection_reset");
    expect(networkReason(new Error("secret token"))).toBe("other");
  });

  it("contains sink failures without unhandled errors", async () => {
    const record = networkDiagnosticSink(async () => { throw new Error("disk unavailable"); });
    record({ event: "bridge_connected", connection: 1 });
    await tick();
  });
});

it("observes upstream end and ignores observer failures without changing relay", async () => {
  const backend = net.createServer(socket => socket.once("data", () => socket.end("fixture")));
  await new Promise<void>(resolve => backend.listen(0, "127.0.0.1", resolve));
  const address = backend.address();
  if (!address || typeof address === "string") throw new Error("fixture did not listen");
  const events: NetworkDiagnostic[] = [];
  const bridge = await startSystemBridge(async () => "DIRECT", event => {
    events.push(event);
    if (event.event === "bridge_route_selected") throw new Error("diagnostic sink failure");
  });
  const target = new URL(bridge.url);
  const socket = net.connect(Number(target.port), target.hostname);
  try {
    const connected = new Promise<string>((resolve, reject) => { socket.once("data", data => resolve(data.toString())); socket.once("error", reject); });
    socket.write(`CONNECT 127.0.0.1:${address.port} HTTP/1.1\r\nHost: fixture\r\nProxy-Authorization: Basic ${Buffer.from("remote:" + bridge.secret).toString("base64")}\r\n\r\n`);
    expect(await connected).toContain("200");
    const data = new Promise<string>((resolve, reject) => { socket.once("data", chunk => resolve(chunk.toString())); socket.once("error", reject); });
    const closed = new Promise<void>(resolve => socket.once("close", () => resolve()));
    socket.write("request");
    expect(await data).toBe("fixture");
    await closed;
    expect(events.some(event => event.event === "bridge_peer_end" && event.side === "upstream")).toBe(true);
    expect(events.find(event => event.event === "bridge_closed")?.bytesFromUpstream).toBe(7);
    expect(JSON.stringify(events)).not.toContain(bridge.secret);
  } finally {
    socket.destroy(); bridge.close();
    await new Promise<void>(resolve => backend.close(() => resolve()));
  }
});
