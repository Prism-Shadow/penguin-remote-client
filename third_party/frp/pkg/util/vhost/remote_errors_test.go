// Added by Penguin Remote contributors in 2026. See REMOTE_CHANGES.md.
// Copyright 2026 Prism Shadow and Penguin Remote contributors
// SPDX-License-Identifier: Apache-2.0

package vhost

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRemoteFailureMarkers(t *testing.T) {
	for _, tc := range []struct {
		name, marker string
		status       int
		dialErr      error
	}{
		{"no route", "tunnel_unavailable", 404, nil},
		{"connection failed", "tunnel_unavailable", 404, errors.New("work connection unavailable")},
		{"timeout", "tunnel_timeout", 504, &net.DNSError{IsTimeout: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := NewHTTPReverseProxy(HTTPReverseProxyOptions{}, NewRouters())
			if tc.dialErr != nil {
				if err := proxy.Register(RouteConfig{Domain: "test.remote.example.com", CreateConnFn: func(string) (net.Conn, error) { return nil, tc.dialErr }}); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = "test.remote.example.com"
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)
			if w.Code != tc.status || w.Header().Get("X-Penguin-Remote-Error") != tc.marker {
				t.Fatalf("status=%d marker=%s", w.Code, w.Header().Get("X-Penguin-Remote-Error"))
			}
		})
	}
}

func TestHarnessApplicationErrorsHaveNoMarker(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, "Harness application error")
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	proxy := NewHTTPReverseProxy(HTTPReverseProxyOptions{}, NewRouters())
	if err := proxy.Register(RouteConfig{Domain: "test.remote.example.com", CreateConnFn: func(string) (net.Conn, error) { return net.Dial("tcp", u.Host) }}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "test.remote.example.com"
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	if w.Code != 404 || w.Header().Get("X-Penguin-Remote-Error") != "" || w.Body.String() != "Harness application error" {
		t.Fatal("application response marked as an infrastructure failure")
	}
}
