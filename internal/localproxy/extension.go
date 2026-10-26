package localproxy

import (
	"net/http"
	"strings"
)

// Pinned by the Harness browser extension's manifest key. Remote deliberately
// does not inherit Harness's dev-profile allowance for arbitrary extensions.
const harnessExtensionOrigin = "chrome-extension://dodgfhpcbmkjfcbgnoidablfgjjhhmgp"

const (
	extensionPairPath = "/api/builtin-browser/extension/pair"
	extensionWSPath   = "/api/builtin-browser/extension/ws"
)

// These endpoints authenticate with a pairing code or WebSocket subprotocol
// token, including for tools without an Origin. Never lend them a local session.
func extensionEndpoint(r *http.Request) bool {
	if r.URL.EscapedPath() != r.URL.Path {
		return false
	}
	switch r.URL.Path {
	case extensionPairPath:
		return r.Method == http.MethodPost || r.Method == http.MethodOptions
	case extensionWSPath:
		return r.Method == http.MethodGet
	default:
		return false
	}
}

func allowsExtensionOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 || values[0] != harnessExtensionOrigin || !extensionEndpoint(r) {
		return false
	}
	switch r.Method {
	case http.MethodOptions:
		methods := r.Header.Values("Access-Control-Request-Method")
		return len(methods) == 1 && methods[0] == http.MethodPost
	case http.MethodGet:
		return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && headerToken(r.Header, "Connection", "upgrade")
	default:
		return true
	}
}

func headerToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// ReverseProxy removes headers named by Connection after its Director runs.
// Reject attempts to erase the extension's origin or authentication protocol.
func extensionHopHeaders(r *http.Request) bool {
	for _, name := range []string{"Origin", "Sec-WebSocket-Protocol", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
		if headerToken(r.Header, "Connection", name) {
			return true
		}
	}
	return false
}
