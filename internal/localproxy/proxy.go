package localproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Prism-Shadow/penguin-remote-client/internal/harnessapi"
	"github.com/Prism-Shadow/penguin-remote-client/internal/publicerror"
)

const validationCacheTTL = 30 * time.Second

type SessionIssuer interface {
	IssueRemoteSession(context.Context) (harnessapi.RemoteSession, error)
	ValidateSession(context.Context, string) (bool, error)
}

type cachedSession struct {
	validUntil time.Time
}

type Proxy struct {
	listener net.Listener
	server   *http.Server
	issuer   SessionIssuer
	secure   bool
	mode     harnessapi.LoginMode

	mu        sync.Mutex
	validated map[string]cachedSession
	pending   *harnessapi.RemoteSession
	issueMu   sync.Mutex
	closeOnce sync.Once
}

func Start(targetURL, apiURL, publicURL string, issuer SessionIssuer, initial harnessapi.RemoteSession) (*Proxy, error) {
	if issuer == nil {
		return nil, fmt.Errorf("automatic Harness login requires a session issuer")
	}
	return start(targetURL, apiURL, publicURL, issuer, initial, harnessapi.LoginAutomatic)
}

// StartManual forwards only browser-supplied credentials. No local API token,
// session issuer or desktop/browser cookie store is available to this proxy.
func StartManual(targetURL, apiURL, publicURL string) (*Proxy, error) {
	return start(targetURL, apiURL, publicURL, nil, harnessapi.RemoteSession{}, harnessapi.LoginManual)
}

func start(targetURL, apiURL, publicURL string, issuer SessionIssuer, initial harnessapi.RemoteSession, mode harnessapi.LoginMode) (*Proxy, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse local Harness target: %w", err)
	}
	if target.Scheme != "http" || target.Host == "" {
		return nil, fmt.Errorf("local Harness target must be an http URL with a host")
	}
	apiTarget := target
	if apiURL != "" {
		apiTarget, err = url.Parse(apiURL)
		if err != nil || apiTarget.Scheme != "http" || apiTarget.Host == "" {
			return nil, fmt.Errorf("local Harness API target must be an http URL with a host")
		}
	}
	public, err := parseOrigin(strings.TrimSuffix(publicURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse public tunnel origin: %w", err)
	}
	publicAuthority, _ := url.Parse(publicURL)
	split := apiTarget.Host != target.Host
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local Harness proxy: %w", err)
	}
	p := &Proxy{
		listener:  listener,
		issuer:    issuer,
		secure:    public.scheme == "https",
		mode:      mode,
		validated: make(map[string]cachedSession),
		pending:   &initial,
	}
	reverseProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			extension := extensionEndpoint(req)
			upstream := apiTarget
			authority := publicAuthority.Host
			if split && !harnessAPIPath(req.URL.Path) {
				// Vite admits the local dev host. API requests bypass it and retain
				// the public host, so Harness does not emit loopback preview URLs.
				upstream = target
				authority = target.Host
			}
			req.URL.Scheme = upstream.Scheme
			req.URL.Host = upstream.Host
			req.URL.Path = joinURLPath(upstream.Path, req.URL.Path)
			if upstream.RawQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = upstream.RawQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = upstream.RawQuery + "&" + req.URL.RawQuery
			}
			// The origin was checked before any local credentials were added.
			// Preserve the external host while matching the local HTTP transport.
			req.Host = authority
			req.Header.Set("X-Forwarded-Host", authority)
			req.Header.Set("X-Forwarded-Proto", upstream.Scheme)
			req.Header.Del("Forwarded")
			if !extension && req.Header.Get("Origin") != "" {
				req.Header.Set("Origin", upstream.Scheme+"://"+authority)
			}
		},
		// Streaming bypasses flush immediately; ordinary JSON coalesces Flush calls.
		FlushInterval: -1,
		BufferPool:    responseCopyBuffers,
		ModifyResponse: func(response *http.Response) error {
			// This marker is reserved for Remote/frps infrastructure failures.
			// A Harness response must never supply it, including application errors.
			response.Header.Del(publicerror.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			w.Header().Set(publicerror.Header, "harness_unavailable")
			publicerror.Write(w, r, http.StatusBadGateway, "harness_unavailable", "")
		},
	}
	p.server = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			extension := extensionEndpoint(r)
			if (!public.allows(r.Header.Values("Origin")) && !allowsExtensionOrigin(r)) ||
				(extension && extensionHopHeaders(r)) {
				http.Error(w, "request origin does not match this tunnel", http.StatusForbidden)
				return
			}
			if extension {
				// Harness owns code/token validation and revocation. Ambient cookies
				// and Authorization are not part of the extension protocol.
				r.Header.Del("Cookie")
				r.Header.Del("Authorization")
				serveBufferedResponse(reverseProxy, w, r)
				return
			}
			if p.mode == harnessapi.LoginManual {
				serveBufferedResponse(reverseProxy, w, r)
				return
			}
			token, session, replace, err := p.ensureSession(r.Context(), cookieValue(r, harnessapi.SessionCookieName))
			if err != nil {
				w.Header().Set(publicerror.Header, "harness_session_failed")
				publicerror.Write(w, r, http.StatusBadGateway, "harness_session_failed", "")
				return
			}
			if replace {
				http.SetCookie(w, &http.Cookie{
					Name:     harnessapi.SessionCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					Secure:   p.secure,
					SameSite: http.SameSiteLaxMode,
					MaxAge:   session.MaxAgeSeconds,
					Expires:  session.ExpiresAt,
				})
			}
			replaceCookie(r, harnessapi.SessionCookieName, token)
			serveBufferedResponse(reverseProxy, w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = p.server.Serve(listener)
	}()
	return p, nil
}

func harnessAPIPath(path string) bool {
	if path == "/api" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/preview/") {
		return true
	}
	if strings.HasPrefix(path, "/server/") {
		parts := strings.SplitN(strings.TrimPrefix(path, "/server/"), "/", 2)
		return len(parts) == 2 && (parts[1] == "api" || strings.HasPrefix(parts[1], "api/"))
	}
	return false
}

func (p *Proxy) Backend() (string, int) {
	addr := p.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func (p *Proxy) Close(ctx context.Context) error {
	var err error
	p.closeOnce.Do(func() {
		err = p.server.Shutdown(ctx)
	})
	return err
}

func (p *Proxy) ensureSession(ctx context.Context, existing string) (string, harnessapi.RemoteSession, bool, error) {
	now := time.Now()
	if existing != "" {
		p.mu.Lock()
		cached, ok := p.validated[existing]
		p.mu.Unlock()
		if ok && now.Before(cached.validUntil) {
			return existing, harnessapi.RemoteSession{}, false, nil
		}
		valid, err := p.issuer.ValidateSession(ctx, existing)
		if err != nil {
			return "", harnessapi.RemoteSession{}, false, err
		}
		if valid {
			p.remember(existing, now.Add(validationCacheTTL))
			return existing, harnessapi.RemoteSession{}, false, nil
		}
	}

	p.issueMu.Lock()
	defer p.issueMu.Unlock()
	p.mu.Lock()
	if p.pending != nil {
		session := *p.pending
		p.pending = nil
		p.mu.Unlock()
		if remaining, ok := sessionForResponse(session, now); ok {
			p.remember(remaining.SessionToken, minTime(remaining.ExpiresAt, now.Add(validationCacheTTL)))
			return remaining.SessionToken, remaining, true, nil
		}
	} else {
		p.mu.Unlock()
	}

	session, err := p.issuer.IssueRemoteSession(ctx)
	if err != nil {
		return "", harnessapi.RemoteSession{}, false, err
	}
	now = time.Now()
	remaining, ok := sessionForResponse(session, now)
	if !ok {
		return "", harnessapi.RemoteSession{}, false, fmt.Errorf("Harness issued an already expired remote session")
	}
	p.remember(remaining.SessionToken, minTime(remaining.ExpiresAt, now.Add(validationCacheTTL)))
	return remaining.SessionToken, remaining, true, nil
}

func (p *Proxy) remember(token string, validUntil time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for existing, cached := range p.validated {
		if !now.Before(cached.validUntil) {
			delete(p.validated, existing)
		}
	}
	p.validated[token] = cachedSession{validUntil: validUntil}
}

func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func replaceCookie(r *http.Request, name, value string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != name {
			r.AddCookie(cookie)
		}
	}
	r.AddCookie(&http.Cookie{Name: name, Value: value})
}

func joinURLPath(base, request string) string {
	slash := strings.HasSuffix(base, "/") || strings.HasPrefix(request, "/")
	switch {
	case strings.HasSuffix(base, "/") && strings.HasPrefix(request, "/"):
		return base + request[1:]
	case !slash:
		return base + "/" + request
	default:
		return base + request
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func sessionForResponse(session harnessapi.RemoteSession, now time.Time) (harnessapi.RemoteSession, bool) {
	remaining := int(session.ExpiresAt.Sub(now) / time.Second)
	if remaining <= 0 {
		return harnessapi.RemoteSession{}, false
	}
	if session.MaxAgeSeconds <= 0 || session.MaxAgeSeconds > remaining {
		session.MaxAgeSeconds = remaining
	}
	return session, true
}
