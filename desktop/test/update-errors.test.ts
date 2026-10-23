import { describe, expect, it } from "vitest";
import { canRetryUpdateSource, UpdateIntegrityError, WindowsUpdateVerificationError } from "../src/update-errors.js";

describe("Electron source-fallback error classification", () => {
  it.each([
    "ERR_ADDRESS_UNREACHABLE", "ERR_HTTP2_PROTOCOL_ERROR", "ERR_CONTENT_LENGTH_MISMATCH",
    "ERR_EMPTY_RESPONSE", "ERR_INCOMPLETE_CHUNKED_ENCODING", "ERR_RESPONSE_HEADERS_TRUNCATED",
    "ERR_HTTP2_SERVER_REFUSED_STREAM", "ERR_HTTP2_PING_FAILED", "ERR_QUIC_PROTOCOL_ERROR",
    "ERR_PROXY_CONNECTION_FAILED", "ERR_TUNNEL_CONNECTION_FAILED", "ERR_SOCKS_CONNECTION_FAILED",
    "ERR_SOCKS_CONNECTION_HOST_UNREACHABLE", "ERR_NAME_RESOLUTION_FAILED", "ERR_SOCKET_NOT_CONNECTED",
    "ERR_NETWORK_IO_SUSPENDED", "ERR_CONNECTION_RESET", "ERR_NAME_NOT_RESOLVED", "ERR_TIMED_OUT",
  ])("recognizes %s in code properties and Electron messages", code => {
    expect(canRetryUpdateSource(Object.assign(new Error("request failed"), { code }))).toBe(true);
    expect(canRetryUpdateSource(Object.assign(new Error("request failed"), { code: `net::${code}` }))).toBe(true);
    expect(canRetryUpdateSource(new Error(`net::${code}`))).toBe(true);
    expect(canRetryUpdateSource(new Error(`net::${code} at https://example.com/installer.exe`))).toBe(true);
  });

  it.each([
    "ERR_CERT_AUTHORITY_INVALID", "ERR_CERT_DATE_INVALID", "ERR_CERT_COMMON_NAME_INVALID",
    "ERR_PROXY_CERTIFICATE_INVALID", "ERR_SSL_PROTOCOL_ERROR", "ERR_HTTP2_INADEQUATE_TRANSPORT_SECURITY",
    "ERR_ABORTED", "ERR_BLOCKED_BY_CLIENT", "ERR_FAILED", "ERR_UNKNOWN_NETWORK_ERROR",
    "ERR_HTTP2_PROTOCOL_ERROR_UNKNOWN", "ERR_CONNECTION_UNKNOWN_NETWORK_ERROR",
  ])("does not retry certificate, security, cancellation or unknown failures: %s", code => {
    expect(canRetryUpdateSource(Object.assign(new Error("request failed"), { code }))).toBe(false);
    expect(canRetryUpdateSource(new Error(`net::${code}`))).toBe(false);
    expect(canRetryUpdateSource(new Error(`net::${code} during network request`))).toBe(false);
  });

  it.each([
    new UpdateIntegrityError("Different release version"), new WindowsUpdateVerificationError(),
    Object.assign(new Error("sha512 checksum mismatch"), { code: "ERR_CONTENT_LENGTH_MISMATCH" }),
    Object.assign(new Error("Windows update signature could not be verified."), { code: "ERR_HTTP2_PROTOCOL_ERROR" }),
    Object.assign(new Error("different version"), { code: "ERR_UPDATER_INVALID_VERSION" }),
    Object.assign(new Error("checksum mismatch"), { statusCode: 503 }),
  ])("keeps integrity checks ahead of transport retry (%#)", error => {
    expect(canRetryUpdateSource(error)).toBe(false);
  });

  it("keeps explicit certificate errors ahead of generic network diagnostics", () => {
    expect(canRetryUpdateSource(Object.assign(new Error("network request failed"), { code: "ERR_CERT_DATE_INVALID" }))).toBe(false);
    expect(canRetryUpdateSource(Object.assign(new Error("net::ERR_CERT_AUTHORITY_INVALID"), { code: "ECONNRESET" }))).toBe(false);
  });
});
