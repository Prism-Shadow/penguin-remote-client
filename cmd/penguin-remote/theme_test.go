package main

import (
	"bytes"
	"io"
	"net/http"
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
