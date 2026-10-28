package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppThemePreferencePersistsAcrossStarts(t *testing.T) {
	app, _, server := newTestApp(t, nil, "")
	defer server.Close()
	if got := loadTheme(app.configPath); got != "system" {
		t.Fatalf("default theme = %q, want system", got)
	}

	for _, theme := range []string{"dark", "light", "system"} {
		response, err := http.Post(server.URL+"/api/preferences/theme", "application/json", strings.NewReader(`{"theme":"`+theme+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("theme %s status = %s, want 200", theme, response.Status)
		}
		if got := loadTheme(app.configPath); got != theme {
			t.Fatalf("stored theme = %q, want %q", got, theme)
		}
		if got := fetchStatus(t, server)["theme"]; got != theme {
			t.Fatalf("status theme = %v, want %q", got, theme)
		}
	}
}

func TestAppThemePreferenceRejectsUnknownValue(t *testing.T) {
	app, _, server := newTestApp(t, nil, "")
	defer server.Close()
	response, err := http.Post(server.URL+"/api/preferences/theme", "application/json", strings.NewReader(`{"theme":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %s, want 400", response.Status)
	}
	if got := loadTheme(app.configPath); got != "system" {
		t.Fatalf("stored theme = %q, want system", got)
	}
}

func TestAppIndexUsesSavedThemeBeforeStatusRequests(t *testing.T) {
	app, _, server := newTestApp(t, nil, "")
	for _, theme := range []string{"dark", "light", "system"} {
		t.Run(theme, func(t *testing.T) {
			post(t, server, "/api/preferences/theme", `{"theme":"`+theme+`"}`)
			// Exercise both reopening the current service's page and a fresh start.
			restarted := &appServer{theme: loadTheme(app.configPath)}
			for _, instance := range []*appServer{app, restarted} {
				for open := 0; open < 2; open++ {
					response := httptest.NewRecorder()
					instance.handleIndex(response, httptest.NewRequest(http.MethodGet, "/", nil))
					want := `<html lang="zh-CN" data-theme="` + theme + `">`
					if !strings.Contains(response.Body.String(), want) {
						t.Fatalf("initial HTML does not contain %s", want)
					}
					if got := response.Header().Get("Cache-Control"); got != "no-store" {
						t.Fatalf("theme-dependent HTML cache control = %q, want no-store", got)
					}
				}
			}
		})
	}
}

func TestAppIndexNormalizesUnknownTheme(t *testing.T) {
	app := &appServer{theme: `dark" onload="alert(1)`}
	response := httptest.NewRecorder()
	app.handleIndex(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(response.Body.String(), `<html lang="zh-CN" data-theme="system">`) {
		t.Fatal("unknown theme must fall back to system in initial HTML")
	}
}

func TestAppServesEmbeddedSupportQRCode(t *testing.T) {
	_, _, server := newTestApp(t, nil, "")
	defer server.Close()
	response, err := http.Get(server.URL + "/assets/wechat-support-qr.png")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("QR response = %s, %q", response.Status, response.Header.Get("Content-Type"))
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, supportWechatQR) || !bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("served QR code differs from the embedded PNG")
	}
}
