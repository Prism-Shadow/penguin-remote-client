package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestAppLanguagePreferencePersistsAcrossStarts(t *testing.T) {
	app, _, server := newTestApp(t, nil, "")
	defer server.Close()
	if got := loadLanguage(app.configPath); got != "zh" {
		t.Fatalf("default language = %q, want zh", got)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/preferences/language", strings.NewReader(`{"language":"en"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200", response.Status)
	}
	if got := loadLanguage(app.configPath); got != "en" {
		t.Fatalf("stored language = %q, want en", got)
	}
	if got := fetchStatus(t, server)["language"]; got != "en" {
		t.Fatalf("status language = %v, want en", got)
	}
}

func TestAppLanguagePreferenceRejectsUnknownValue(t *testing.T) {
	app, _, server := newTestApp(t, nil, "")
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/preferences/language", strings.NewReader(`{"language":"other"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %s, want 400", response.Status)
	}
	if got := loadLanguage(app.configPath); got != "zh" {
		t.Fatalf("stored language = %q, want zh", got)
	}
}
