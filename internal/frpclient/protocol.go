package frpclient

import (
	"github.com/Prism-Shadow/penguin-remote-client/internal/remoteapi"
	"strings"
)

type ProtocolDenial struct {
	Code         string
	Operation    string
	RetryAllowed *bool
}

func (d *ProtocolDenial) Error() string  { return d.Code }
func (d *ProtocolDenial) Reason() string { return d.Code }
func (d *ProtocolDenial) Retryable() bool {
	if d.RetryAllowed != nil {
		return *d.RetryAllowed
	}
	return d.Code == "service_unavailable" || d.Code == "plugin_secret_not_configured" || d.Code == "proxy_wait"
}
func (r *Runner) protocolError(op, code string) {
	if !strings.HasPrefix(code, "penguin_remote:") {
		return
	}
	reason := strings.TrimPrefix(code, "penguin_remote:")
	var retryAllowed *bool
	if base, mode, found := strings.Cut(reason, ":"); found {
		if mode != "retry" && mode != "stop" {
			return
		}
		allowed := mode == "retry"
		retryAllowed = &allowed
		reason = base
	}
	if !remoteapi.SafeReasons[reason] {
		return
	}
	d := &ProtocolDenial{Code: reason, Operation: op, RetryAllowed: retryAllowed}
	reportProtocolDiagnostic(op, d.Code, d.Retryable())
	if d.Retryable() {
		r.cancelMu.Lock()
		r.protocolStatus = reason
		r.cancelMu.Unlock()
		return
	}
	select {
	case r.denial <- d:
	default:
	}
	r.cancelMu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.cancelMu.Unlock()
}

func (r *Runner) ConnectionPhase() string {
	if r.ProxyConnected() {
		return "connected"
	}
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	if r.protocolStatus == "proxy_wait" {
		return "proxy_wait"
	}
	return ""
}
