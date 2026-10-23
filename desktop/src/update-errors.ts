/** Signature invalidation removes the cached installer; recovery starts with a new check. */
export class WindowsUpdateVerificationError extends Error {
  constructor() {
    super("Windows update verification failed. Check for updates and download it again.");
    this.name = "WindowsUpdateVerificationError";
  }
}

export class UpdateIntegrityError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "UpdateIntegrityError";
  }
}

export function isUpdateIntegrityError(error: unknown): boolean {
  if (error instanceof UpdateIntegrityError || error instanceof WindowsUpdateVerificationError) return true;
  if (!(error instanceof Error)) return false;
  const code = (error as Error & { code?: string }).code ?? "";
  return /CHECKSUM|DIGEST|SIGNATURE|INVALID_UPDATE_INFO|INVALID_VERSION|NO_CHECKSUM/.test(code) ||
    /checksum|sha-?512|sha-?256|signature|not signed|code.?sign/i.test(error.message);
}

// Chromium net/base/net_error_list.h: connection, protocol and interrupted-body
// failures can try the other source. Certificate/security and cancellation errors
// are deliberately excluded. Share this list between code and message handling.
const RETRYABLE_ELECTRON_TRANSPORT_ERRORS = new Set([
  "ERR_CONNECTION_CLOSED", "ERR_CONNECTION_RESET", "ERR_CONNECTION_REFUSED",
  "ERR_CONNECTION_ABORTED", "ERR_CONNECTION_FAILED", "ERR_CONNECTION_TIMED_OUT",
  "ERR_ADDRESS_UNREACHABLE", "ERR_NAME_NOT_RESOLVED", "ERR_NAME_RESOLUTION_FAILED",
  "ERR_NETWORK_CHANGED", "ERR_INTERNET_DISCONNECTED", "ERR_NETWORK_IO_SUSPENDED",
  "ERR_TIMED_OUT", "ERR_SOCKET_NOT_CONNECTED", "ERR_PROXY_CONNECTION_FAILED",
  "ERR_TUNNEL_CONNECTION_FAILED", "ERR_SOCKS_CONNECTION_FAILED", "ERR_SOCKS_CONNECTION_HOST_UNREACHABLE",
  "ERR_HTTP2_PROTOCOL_ERROR", "ERR_HTTP2_SERVER_REFUSED_STREAM", "ERR_HTTP2_PING_FAILED",
  "ERR_QUIC_PROTOCOL_ERROR", "ERR_EMPTY_RESPONSE", "ERR_CONTENT_LENGTH_MISMATCH",
  "ERR_INCOMPLETE_CHUNKED_ENCODING", "ERR_RESPONSE_HEADERS_TRUNCATED",
]);

/** Only transport/availability failures may switch sources; unknown errors stop. */
export function canRetryUpdateSource(error: unknown): boolean {
  if (isUpdateIntegrityError(error) || !(error instanceof Error)) return false;
  const { code, statusCode } = error as Error & { code?: string; statusCode?: number };
  const normalizedCode = code?.replace(/^net::/, "");
  const electronCode = /\bnet::(ERR_[A-Z0-9_]+)\b/.exec(error.message)?.[1] ??
    (normalizedCode?.startsWith("ERR_") ? normalizedCode : undefined);
  if (electronCode !== undefined) {
    // A named failure takes precedence over vague words such as "network" in
    // its surrounding message, so a certificate/unknown error cannot slip through.
    return electronCode === "ERR_UPDATER_CHANNEL_FILE_NOT_FOUND" || electronCode === "ERR_STREAM_NOT_FINISHED" ||
      RETRYABLE_ELECTRON_TRANSPORT_ERRORS.has(electronCode);
  }
  if (statusCode !== undefined) return statusCode === 404 || statusCode === 408 || statusCode === 429 || statusCode >= 500 && statusCode <= 599;
  // builder-util-runtime's package downloader uses a plain Error for HTTP
  // failures, unlike its metadata requests which expose statusCode.
  const downloadStatus = /^Cannot download ".+", status (\d{3}):/.exec(error.message)?.[1];
  if (downloadStatus) {
    const status = Number(downloadStatus);
    return status === 404 || status === 408 || status === 429 || status >= 500 && status <= 599;
  }
  return /^(ECONNRESET|ECONNREFUSED|ECONNABORTED|ETIMEDOUT|ENOTFOUND|EAI_AGAIN|EHOSTUNREACH|ENETUNREACH)$/.test(code ?? "") ||
    /\b(network|offline|timed? ?out|socket hang up|Request has been aborted by the server)\b/i.test(error.message);
}
