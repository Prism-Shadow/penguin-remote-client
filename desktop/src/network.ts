import http from "node:http";
import net from "node:net";
import tls from "node:tls";
import crypto from "node:crypto";
import type { Duplex } from "node:stream";
import { networkReason, type NetworkObserver } from "./network-diagnostics.js";
function notify(observer: NetworkObserver | undefined, event: Parameters<NetworkObserver>[0]): void {
  try { observer?.(event); } catch { /* Diagnostics cannot interrupt network handling. */ }
}
export type Resolver = (url: string) => Promise<string>;
export function systemRoute(result: string): { kind: string; host?: string; port?: number } {
  const first = result.split(";")[0]?.trim() || "";
  if (first === "DIRECT") return { kind: "DIRECT" };
  const match = /^(PROXY|HTTP|HTTPS|SOCKS5|SOCKS) (.+):(\d+)$/.exec(first);
  if (!match || Number(match[3]) < 1 || Number(match[3]) > 65535) throw new Error("system_proxy_unavailable");
  return { kind: match[1]!, host: match[2]!, port: Number(match[3]) };
}
function connected(host: string, port: number, secure = false): Promise<net.Socket> {
  return new Promise((resolve, reject) => {
    const s = secure ? tls.connect({ host, port, servername: host }) : net.connect({ host, port });
    s.setTimeout(15000, () => s.destroy(new Error("network_timeout")));
    s.once("error", reject);
    s.once(secure ? "secureConnect" : "connect", () => { s.removeListener("error", reject); resolve(s); });
  });
}
async function readUntil(s: net.Socket, done: (data: Buffer) => number): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    let data: Buffer = Buffer.alloc(0);
    const cleanup = () => { s.off("data", onData); s.off("error", fail); s.off("close", fail); };
    const fail = () => { cleanup(); reject(new Error("proxy_failed")); };
    const onData = (chunk: Buffer) => {
      data = Buffer.concat([data, chunk]); if (data.length > 16384) { fail(); return; }
      const n = done(data);
      if (n > 0) { cleanup(); s.pause(); if (data.length > n) s.unshift(data.subarray(n)); resolve(data.subarray(0, n)); }
    };
    s.on("data", onData); s.once("error", fail); s.once("close", fail); s.resume();
  });
}
async function tunnel(resolveProxy: Resolver, host: string, port: number, canceled: () => boolean, observer?: NetworkObserver, connection = 0): Promise<net.Socket> {
  const routes = (await resolveProxy("https://" + (host.includes(":") ? "[" + host + "]" : host) + ":" + port)).split(";").map(s=>s.trim()).filter(Boolean);
  for (const entry of routes) {
   if (canceled()) throw new Error("system_proxy_unavailable");
   const started = Date.now();
   let kind: string | undefined;
   try {
     const route = systemRoute(entry); kind = route.kind;
     notify(observer, { event: "bridge_route_selected", connection, kind });
     return await routedTunnel(route,host,port);
   } catch (error) {
     notify(observer, { event: "bridge_route_failed", connection, ...(kind ? { kind } : {}), reason: networkReason(error), durationMs: Date.now() - started });
     /* Only PAC-declared fallback routes are considered. */
   }
  }
  throw new Error("system_proxy_unavailable");
}
async function routedTunnel(route:ReturnType<typeof systemRoute>,host:string,port:number):Promise<net.Socket>{
  if (route.kind === "DIRECT") { const socket=await connected(host,port);socket.setTimeout(0);return socket; }
  const socket = await connected(route.host!, route.port!, route.kind === "HTTPS");
  try {
    if (route.kind === "SOCKS" || route.kind === "SOCKS5") {
      const greeting = readUntil(socket, b => b.length >= 2 ? 2 : 0); socket.write(Buffer.from([5, 1, 0]));
      const reply = await greeting; if (reply[0] !== 5 || reply[1] !== 0) throw new Error("proxy_failed");
      const hostname = Buffer.from(host); if (hostname.length > 255) throw new Error("proxy_failed");
      const request = Buffer.concat([Buffer.from([5, 1, 0, 3, hostname.length]), hostname, Buffer.from([port >> 8, port & 255])]);
      const response = readUntil(socket, b => { if (b.length < 5) return 0; const len = b[3] === 1 ? 10 : b[3] === 4 ? 22 : b[3] === 3 ? 7 + b[4]! : 0; return len && b.length >= len ? len : 0; });
      socket.write(request); const r = await response; if (r[0] !== 5 || r[1] !== 0) throw new Error("proxy_failed");
    } else {
      const authority = (host.includes(":") ? "[" + host + "]" : host) + ":" + port;
      const response = readUntil(socket, b => { const n = b.indexOf("\r\n\r\n"); return n >= 0 ? n + 4 : 0; });
      socket.write("CONNECT " + authority + " HTTP/1.1\r\nHost: " + authority + "\r\n\r\n");
      const r = await response; if (!/^HTTP\/1\.[01] 200(?: |\r)/.test(r.toString())) throw new Error("proxy_failed");
    }
    socket.setTimeout(0); return socket;
  } catch { socket.destroy(); throw new Error("proxy_failed"); }
}
export async function startSystemBridge(resolveProxy: Resolver, observer?: NetworkObserver): Promise<{ url: string; secret: string; close: () => void }> {
  const secret = crypto.randomBytes(32).toString("hex");
  const auth = "Basic " + Buffer.from("remote:" + secret).toString("base64");
  const sockets = new Set<Duplex>();
  let closed = false;
  let nextConnection = 0;
  const server = http.createServer((_req, res) => { res.writeHead(405); res.end(); });
  server.on("connect", (req, client, head) => { void (async () => {
    sockets.add(client);
    client.on("error", () => client.destroy());
    client.once("close", () => sockets.delete(client));
    if (req.headers["proxy-authorization"] !== auth) { client.end("HTTP/1.1 407 Proxy Authentication Required\r\n\r\n"); return; }
    const connection = ++nextConnection;
    const started = Date.now();
    try {
      const target = new URL("http://" + req.url); const port = Number(target.port);
      if (!port || target.username || target.password || target.pathname !== "/" || target.search || target.hash) throw new Error();
      const upstream = await tunnel(resolveProxy, target.hostname.replace(/^\[|\]$/g, ""), port, () => closed || client.destroyed, observer, connection);
      if (closed || client.destroyed) { upstream.destroy(); client.destroy(); return; }
      sockets.add(client); sockets.add(upstream);
      let ended = false;
      let bytesFromLocal = head.length;
      let bytesFromUpstream = 0;
      const close = (side: "local" | "upstream", error?: unknown) => {
        if (!ended) {
          ended = true;
          notify(observer, { event: "bridge_closed", connection, side, reason: closed ? "shutdown" : error ? networkReason(error) : side === "local" ? "local_closed" : "upstream_closed", durationMs: Date.now() - started, bytesFromLocal, bytesFromUpstream });
        }
        client.destroy(); upstream.destroy(); sockets.delete(client); sockets.delete(upstream);
      };
      client.on("error", error => close("local", error)); upstream.on("error", error => close("upstream", error));
      client.on("close", () => close("local")); upstream.on("close", () => close("upstream"));
      if (observer) {
        client.once("end", () => notify(observer, { event: "bridge_peer_end", connection, side: "local" }));
        upstream.once("end", () => notify(observer, { event: "bridge_peer_end", connection, side: "upstream" }));
        client.on("data", chunk => { bytesFromLocal += chunk.length; });
        upstream.on("data", chunk => { bytesFromUpstream += chunk.length; });
        for (const [socket, side] of [[client, "local"], [upstream, "upstream"]] as const) {
          let paused = 0;
          socket.on("pause", () => { paused = Date.now(); });
          socket.on("resume", () => {
            if (paused && Date.now() - paused >= 250) notify(observer, { event: "bridge_backpressure", connection, side, durationMs: Date.now() - paused });
            paused = 0;
          });
        }
      }
      notify(observer, { event: "bridge_connected", connection, durationMs: Date.now() - started });
      client.write("HTTP/1.1 200 Connection Established\r\n\r\n"); if (head.length) upstream.write(head);
      client.pipe(upstream); upstream.pipe(client); upstream.resume();
    } catch (error) {
      notify(observer, { event: "bridge_closed", connection, reason: closed ? "shutdown" : networkReason(error), durationMs: Date.now() - started });
      client.end("HTTP/1.1 502 Bad Gateway\r\n\r\n");
    }
  })(); });
  await new Promise<void>((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  const address = server.address(); if (address === null || typeof address === "string") throw new Error("system_proxy_unavailable");
  return { url: "http://127.0.0.1:" + address.port, secret, close: () => { closed = true; for (const s of sockets) s.destroy(); server.close(); } };
}
