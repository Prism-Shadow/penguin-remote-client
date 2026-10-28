package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
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

func TestAppIndexLocalizesSavedLanguageBeforeScriptsRun(t *testing.T) {
	app, _, server := newTestApp(t, nil, "")
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			post(t, server, "/api/preferences/language", `{"language":"`+language+`"}`)
			for _, theme := range []string{"dark", "light", "system"} {
				post(t, server, "/api/preferences/theme", `{"theme":"`+theme+`"}`)
				restarted := &appServer{language: loadLanguage(app.configPath), theme: loadTheme(app.configPath)}
				for _, instance := range []*appServer{app, restarted} {
					response := httptest.NewRecorder()
					instance.handleIndex(response, httptest.NewRequest(http.MethodGet, "/", nil))
					document, err := html.Parse(strings.NewReader(response.Body.String()))
					if err != nil {
						t.Fatal(err)
					}
					wantLang, wantTheme, wantLogin := "zh-CN", "主题", "登录 Penguin Go"
					wantPreferences, wantPlaceholder := "偏好设置", "例如 http://localhost:7364"
					if language == "en" {
						wantLang, wantTheme, wantLogin = "en", "Theme", "Sign in to Penguin Go"
						wantPreferences, wantPlaceholder = "Preferences", "For example http://localhost:7364"
					}
					checks := 0
					var visit func(*html.Node)
					visit = func(node *html.Node) {
						attrs := make(map[string]string)
						for _, attr := range node.Attr {
							attrs[attr.Key] = attr.Val
						}
						if node.Type == html.ElementNode && node.Data == "html" {
							if attrs["lang"] != wantLang || attrs["data-theme"] != theme {
								t.Fatalf("initial HTML preferences = %v", attrs)
							}
							checks++
						}
						switch attrs["id"] {
						case "themeLabel", "login":
							want := wantTheme
							if attrs["id"] == "login" {
								want = wantLogin
							}
							if node.FirstChild == nil || node.FirstChild.Type != html.TextNode || node.FirstChild.Data != want {
								t.Fatalf("initial text for %s is not %q", attrs["id"], want)
							}
							checks++
						case "preferencesButton":
							if attrs["aria-label"] != wantPreferences || attrs["title"] != wantPreferences {
								t.Fatalf("initial preference attributes = %v", attrs)
							}
							checks++
						case "manualLocalUrl":
							if attrs["placeholder"] != wantPlaceholder {
								t.Fatalf("initial placeholder = %q, want %q", attrs["placeholder"], wantPlaceholder)
							}
							checks++
						}
						for child := node.FirstChild; child != nil; child = child.NextSibling {
							visit(child)
						}
					}
					visit(document)
					if checks != 5 {
						t.Fatalf("checked %d localized elements, want 5", checks)
					}
				}
			}
		})
	}
}

func TestAppIndexNormalizesUnknownLanguage(t *testing.T) {
	app := &appServer{language: `en" onload="alert(1)`}
	response := httptest.NewRecorder()
	app.handleIndex(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(response.Body.String(), `<html lang="zh-CN" data-theme="system">`) {
		t.Fatal("unknown language must fall back to Chinese in initial HTML")
	}
}
