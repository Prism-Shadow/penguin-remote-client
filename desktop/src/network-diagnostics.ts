import fs from "node:fs/promises";

export type NetworkDiagnostic = {
  event: "bridge_route_selected" | "bridge_route_failed" | "bridge_connected" | "bridge_peer_end" | "bridge_closed" | "bridge_backpressure";
  connection: number;
  kind?: string;
  reason?: string;
  side?: "local" | "upstream";
  durationMs?: number;
  bytesFromLocal?: number;
  bytesFromUpstream?: number;
};
export type NetworkObserver = (event: NetworkDiagnostic) => void;

export function networkReason(error: unknown): string {
  const code = (error as { code?: unknown })?.code;
  switch (code) {
    case "ECONNRESET": return "connection_reset";
    case "ECONNREFUSED": return "connection_refused";
    case "ETIMEDOUT": return "timeout";
    case "EPIPE": return "broken_pipe";
    case "ENOTFOUND": case "EAI_AGAIN": return "dns_failed";
    case "ERR_TLS_CERT_ALTNAME_INVALID": case "CERT_HAS_EXPIRED": case "UNABLE_TO_VERIFY_LEAF_SIGNATURE": return "tls_failed";
  }
  if (error instanceof Error && error.message === "network_timeout") return "timeout";
  return "other";
}

const events = new Set(["bridge_route_selected", "bridge_route_failed", "bridge_connected", "bridge_peer_end", "bridge_closed", "bridge_backpressure"]);
const reasons = new Set(["", "connection_reset", "connection_refused", "timeout", "broken_pipe", "dns_failed", "tls_failed", "other", "local_closed", "upstream_closed", "shutdown"]);

export function networkDiagnosticSink(append: (line: string) => Promise<void>): NetworkObserver {
  const queue: string[] = [];
  let busy = false;
  let dropped = 0;
  const write = async (line: string) => { try { await append(line); } catch { /* Output cannot affect relay traffic. */ } };
  const drain = async () => {
    if (busy) return;
    busy = true;
    try {
      while (queue.length) {
        const line = queue.shift()!;
        if (dropped) {
          const count = dropped;
          dropped = 0;
          await write(JSON.stringify({ time: new Date().toISOString(), component: "desktop_network", event: "diagnostic_events_dropped", droppedEvents: count }) + "\n");
        }
        await write(line);
      }
    } finally { busy = false; }
  };
  return (event) => {
    if (!events.has(event.event) || !reasons.has(event.reason ?? "")) return;
    if (!Number.isSafeInteger(event.connection) || event.connection < 1) return;
    const numeric = (value: number | undefined) => value !== undefined && Number.isFinite(value) && value >= 0 ? value : undefined;
    const line = JSON.stringify({
      time: new Date().toISOString(), component: "desktop_network", event: event.event,
      connection: event.connection, reason: event.reason,
      kind: ["DIRECT", "PROXY", "HTTP", "HTTPS", "SOCKS", "SOCKS5"].includes(event.kind ?? "") ? event.kind : undefined,
      side: event.side === "local" || event.side === "upstream" ? event.side : undefined,
      durationMs: numeric(event.durationMs), bytesFromLocal: numeric(event.bytesFromLocal), bytesFromUpstream: numeric(event.bytesFromUpstream),
    }) + "\n";
    if (queue.length >= 128) { dropped++; return; }
    queue.push(line);
    void drain();
  };
}

export function createNetworkDiagnostics(file: string): NetworkObserver {
  return networkDiagnosticSink(async (line) => {
    const size = await fs.stat(file).then(stat => stat.size, () => 0);
    if (size + Buffer.byteLength(line) > 2 * 1024 * 1024) {
      await fs.rm(file + ".1", { force: true });
      await fs.rename(file, file + ".1");
    }
    await fs.appendFile(file, line, "utf8");
  });
}
