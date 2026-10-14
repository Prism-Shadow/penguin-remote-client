package publicerror

import (
	"encoding/json"
	"html"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPageCatalogAndLanguage(t *testing.T) {
	for code, m := range messages {
		for _, language := range []string{"zh-CN", "en-US"} {
			t.Run(code+"/"+language, func(t *testing.T) {
				req := httptest.NewRequest("GET", "/workspace?remote_code=private-code&x=1", nil)
				req.Header.Set("Accept", "text/html")
				req.Header.Set("Accept-Language", language)
				w := httptest.NewRecorder()
				Write(w, req, m.status, code, "https://go.example.com/login?return_to=%2Fworkspace")
				title := m.zhTitle
				if language == "en-US" {
					title = m.enTitle
				}
				if w.Code != m.status || !strings.Contains(w.Body.String(), title) || !strings.Contains(html.UnescapeString(w.Body.String()), "data:image/svg+xml;base64,") {
					t.Fatalf("unexpected page: status=%d title=%q", w.Code, title)
				}
				if strings.Contains(w.Body.String(), "private-code") {
					t.Fatal("one-time code leaked into the page")
				}
				if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
					t.Fatal("missing privacy headers")
				}
			})
		}
	}
}

func TestNonNavigationsReceiveJSON(t *testing.T) {
	for _, tc := range []struct{ method, path, accept, upgrade string }{
		{"GET", "/api/session", "text/html", ""},
		{"GET", "/", "application/json", ""},
		{"GET", "/", "text/event-stream, text/html", ""},
		{"GET", "/", "text/html", "websocket"},
		{"POST", "/", "text/html", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Accept", tc.accept)
		req.Header.Set("Upgrade", tc.upgrade)
		w := httptest.NewRecorder()
		Write(w, req, 503, "tunnel_unavailable", "")
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["reason"] != "tunnel_unavailable" || w.Code != 503 {
			t.Fatalf("%+v: invalid API error %s", tc, w.Body.String())
		}
	}
}

func TestUnknownReasonAndUnsafeLinksAreNotExposed(t *testing.T) {
	req := httptest.NewRequest("GET", Path+"?return_to=https%3A%2F%2Fevil.example%2F", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	Write(w, req, 403, "secret-token <script>alert(1)</script>", "javascript:alert(1)")
	for _, private := range []string{"secret-token", "<script>", "javascript:", "evil.example"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("unsafe content exposed: %s", private)
		}
	}
	if !strings.Contains(w.Body.String(), `href="/"`) {
		t.Fatal("missing safe fallback link")
	}
}

func TestReturnPath(t *testing.T) {
	for _, raw := range []string{"https://evil.example/", "//evil.example/", "/%2Fevil.example", "/%5Cevil.example", "/%0D%0Aevil", "javascript:evil", "", Path + "?code=tunnel_unavailable"} {
		if got := ReturnPath(raw); got != "/" {
			t.Fatalf("ReturnPath(%q)=%q", raw, got)
		}
	}
	if got := ReturnPath("/workspace?remote_code=secret&x=1#fragment"); got != "/workspace?x=1" {
		t.Fatalf("return path = %q", got)
	}
}

func TestHEADHasNoBody(t *testing.T) {
	req := httptest.NewRequest("HEAD", "/", nil)
	req.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	Write(w, req, 503, "tunnel_unavailable", "")
	if w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" || w.Code != 503 {
		t.Fatal("invalid HEAD response")
	}
}
