package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The shell owns installation. The local UI only relays fixed update commands.
type desktopUpdateBridge struct {
	url, secret string
	client      *http.Client
}

func newDesktopUpdateBridge(rawURL, secret string) *desktopUpdateBridge {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || secret == "" {
		return nil
	}
	return &desktopUpdateBridge{rawURL, secret, &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (a *appServer) handleDesktopUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	action := strings.TrimPrefix(r.URL.Path, "/api/desktop/update")
	if (action == "" && r.Method != "GET") || (action != "" && r.Method != "POST") {
		http.Error(w, "method not allowed", 405)
		return
	}
	if action != "" && action != "/check" && action != "/download" && action != "/install" {
		http.NotFound(w, r)
		return
	}
	// Browser commands require an exact same-origin POST with an explicit UI header.
	if r.Host != strings.TrimPrefix(a.baseURL, "http://") || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != a.baseURL) ||
		(action != "" && (r.Header.Get("Origin") != a.baseURL || r.Header.Get("X-Penguin-Update") != "1")) {
		http.Error(w, "invalid origin", 403)
		return
	}
	if a.desktopUpdate == nil {
		writeJSON(w, map[string]string{"state": "unsupported", "reason": "no-shell"})
		return
	}
	path := action
	if path == "" {
		path = "/status"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, a.desktopUpdate.url+path, nil)
	if err != nil {
		http.Error(w, "desktop update unavailable", 503)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.desktopUpdate.secret)
	response, err := a.desktopUpdate.client.Do(req)
	if err != nil {
		http.Error(w, "desktop update unavailable", 503)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 202 {
		http.Error(w, "desktop update unavailable", 503)
		return
	}
	if action == "" {
		var status map[string]any
		if err := json.NewDecoder(io.LimitReader(response.Body, 16*1024)).Decode(&status); err != nil || status == nil {
			http.Error(w, "desktop update unavailable", 503)
			return
		}
		a.mu.Lock()
		status["seenVersion"] = a.updateSeenVersion
		a.mu.Unlock()
		writeJSON(w, status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(response.Body, 16*1024))
}
