import { once } from "node:events";
import http from "node:http";
import net from "node:net";
import { describe, expect, it, vi } from "vitest";
import {startSystemBridge, systemRoute} from "../src/network.js";
describe("explicit system proxy bridge",()=>{
 it("uses first PAC route and fails closed on unsupported routes",()=>{
  expect(systemRoute("PROXY proxy.test:8080; DIRECT")).toEqual({kind:"PROXY",host:"proxy.test",port:8080});
  expect(systemRoute("DIRECT")).toEqual({kind:"DIRECT"});
  expect(systemRoute("SOCKS5 proxy.test:1080").kind).toBe("SOCKS5");
  expect(()=>systemRoute("QUIC proxy.test:443; DIRECT")).toThrow();
 });
 it("requires private bridge authentication and applies the resolver to CONNECT",async()=>{
  const backend=net.createServer(s=>s.on("data",b=>s.write(b)));
  await new Promise<void>(r=>backend.listen(0,"127.0.0.1",r));
  const address=backend.address();if(!address||typeof address==="string")throw new Error();
  const seen:string[]=[];const bridge=await startSystemBridge(async u=>{seen.push(u);return "DIRECT";});
  try {
   const u=new URL(bridge.url);const socket=net.connect({host:u.hostname,port:Number(u.port)});
   const response=new Promise<string>((resolve,reject)=>{socket.once("data",b=>resolve(b.toString()));socket.once("error",reject);});
   socket.write("CONNECT 127.0.0.1:"+address.port+" HTTP/1.1\r\nHost: test\r\n\r\n");
   expect(await response).toContain("407");socket.destroy();expect(seen).toEqual([]);
   const authorized=net.connect({host:u.hostname,port:Number(u.port)});
   const auth=Buffer.from("remote:"+bridge.secret).toString("base64");
   const accepted=new Promise<string>((resolve,reject)=>{authorized.once("data",b=>resolve(b.toString()));authorized.once("error",reject);});
   authorized.write("CONNECT 127.0.0.1:"+address.port+" HTTP/1.1\r\nHost: test\r\nProxy-Authorization: Basic "+auth+"\r\n\r\n");
   expect(await accepted).toContain("200");expect(seen).toEqual(["https://127.0.0.1:"+address.port]);
   const echoed=new Promise<string>(r=>authorized.once("data",b=>r(b.toString())));authorized.write("test");expect(await echoed).toBe("test");authorized.destroy();
  } finally {bridge.close();await new Promise<void>(r=>backend.close(()=>r()));}
 });
});

it.each(["DIRECT", "PROXY", "SOCKS5"])("sets a 60-second TCP keepalive idle time on the %s outbound socket", async kind => {
  const target = "frps.test:7000";
  let requestedTarget: string | undefined;
  const connectProxy = http.createServer().on("connect", (req, socket) => {
    requestedTarget = req.url;
    socket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
    socket.pipe(socket);
  });
  const socksProxy = net.createServer(socket => {
    void (async () => {
      const chunks = socket.iterator({ destroyOnReturn: false });
      let pending = Buffer.alloc(0);
      const read = async (size: number) => {
        while (pending.length < size) {
          const chunk = await chunks.next();
          if (chunk.done) throw new Error("SOCKS connection closed during handshake");
          pending = Buffer.concat([pending, chunk.value]);
        }
        const data = pending.subarray(0, size);
        pending = pending.subarray(size);
        return data;
      };
      await read(3);
      socket.write(Buffer.from([5, 0]));
      const header = await read(5);
      const hostname = await read(header[4]!);
      const port = (await read(2)).readUInt16BE();
      requestedTarget = `${hostname}:${port}`;
      await chunks.return?.();
      socket.write(Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 0, 0]));
      socket.pipe(socket);
    })().catch(() => socket.destroy());
  });
  const upstream = kind === "DIRECT" ? net.createServer(socket => socket.pipe(socket))
    : kind === "PROXY" ? connectProxy : socksProxy;
  const upstreamSockets = new Set<net.Socket>();
  upstream.on("connection", socket => upstreamSockets.add(socket));
  upstream.listen(0, "127.0.0.1");
  await once(upstream, "listening");
  const address = upstream.address() as net.AddressInfo;
  const endpoint = `127.0.0.1:${address.port}`;
  const authority = kind === "DIRECT" ? endpoint : target;
  const bridge = await startSystemBridge(async () => kind === "DIRECT" ? kind : `${kind} ${endpoint}`);
  const url = new URL(bridge.url);
  const setKeepAlive = vi.spyOn(net.Socket.prototype, "setKeepAlive");
  const socket = net.connect(Number(url.port), url.hostname);
  try {
    const signal = AbortSignal.timeout(3_000);
    const response = once(socket, "data", { signal });
    const auth = Buffer.from("remote:" + bridge.secret).toString("base64");
    socket.write(`CONNECT ${authority} HTTP/1.1\r\nHost: ${authority}\r\nProxy-Authorization: Basic ${auth}\r\n\r\n`);
    expect(String((await response)[0])).toContain("200 Connection Established");
    if (kind !== "DIRECT") expect(requestedTarget).toBe(target);
    const call = setKeepAlive.mock.contexts.findIndex(
      outbound => outbound instanceof net.Socket && outbound.remoteAddress === "127.0.0.1" && outbound.remotePort === address.port,
    );
    expect(setKeepAlive.mock.calls[call]).toEqual([true, 60_000]);
    const echo = once(socket, "data", { signal });
    socket.write("work connection");
    expect(String((await echo)[0])).toBe("work connection");
  } finally {
    socket.destroy();
    bridge.close();
    setKeepAlive.mockRestore();
    for (const upstreamSocket of upstreamSockets) upstreamSocket.destroy();
    await new Promise<void>(resolve => upstream.close(() => resolve()));
  }
});

it("uses only PAC-declared fallback and never silently connects direct", async () => {
 const backend = net.createServer(socket => socket.end());
 await new Promise<void>(resolve => backend.listen(0, "127.0.0.1", resolve));
 const address = backend.address(); if (!address || typeof address === "string") throw new Error();
 const request = async (route: string) => {
  const bridge = await startSystemBridge(async () => route);
  try {
   const url = new URL(bridge.url);
   const socket = net.connect(Number(url.port), url.hostname);
   const response = new Promise<string>((resolve, reject) => {socket.once("data", data => resolve(data.toString())); socket.once("error", reject);});
   socket.write(`CONNECT 127.0.0.1:${address.port} HTTP/1.1\r\nHost: test\r\nProxy-Authorization: Basic ${Buffer.from("remote:" + bridge.secret).toString("base64")}\r\n\r\n`);
   const result = await response; socket.destroy(); return result;
  } finally { bridge.close(); }
 };
 try {
  expect(await request("QUIC proxy.test:443")).toContain("502");
  expect(await request("QUIC proxy.test:443; DIRECT")).toContain("200");
 } finally { await new Promise<void>(resolve => backend.close(() => resolve())); }
});

it("closing the bridge cancels connections waiting for PAC resolution", async () => {
 let resolveRoute!: (route: string) => void;
 let entered!: () => void;
 const resolving = new Promise<void>(resolve => {entered = resolve;});
 const bridge = await startSystemBridge(async () => {entered(); return await new Promise<string>(resolve => {resolveRoute = resolve;});});
 const url = new URL(bridge.url);
 const socket = net.connect(Number(url.port), url.hostname);
 const closed = new Promise<void>(resolve => socket.once("close", () => resolve()));
 let response = ""; socket.on("data", data => {response += data.toString();});
 socket.write(`CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: test\r\nProxy-Authorization: Basic ${Buffer.from("remote:" + bridge.secret).toString("base64")}\r\n\r\n`);
 await resolving;
 bridge.close();
 await closed;
 resolveRoute("DIRECT");
 await new Promise<void>(resolve => setImmediate(resolve));
 expect(response).not.toContain("200");
});
