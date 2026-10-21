package main

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestAppServesEmbeddedGuideScreenshots(t *testing.T) {
	_, _, server := newTestApp(t, nil, "")
	defer server.Close()
	for _, name := range []string{"account-ready.png", "remote-start.png", "remote-connected.png", "harness-logo.svg"} {
		t.Run(name, func(t *testing.T) {
			response, err := http.Get(server.URL + "/assets/guides/" + name)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			content, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			want, err := guideImages.ReadFile("assets/guides/" + name)
			if err != nil {
				t.Fatal(err)
			}
			contentType := "image/png"
			if name == "harness-logo.svg" {
				contentType = "image/svg+xml"
			} else if !bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("guide asset %s is not a PNG", name)
			}
			if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != contentType || !bytes.Equal(content, want) {
				t.Fatalf("guide asset %s: status=%s, type=%q, bytes=%d", name, response.Status, response.Header.Get("Content-Type"), len(content))
			}
		})
	}
	response, err := http.Get(server.URL + "/assets/guides/missing.png")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing guide asset status = %s, want 404", response.Status)
	}
}
