package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeHarnessRecognizesPenguinHarnessHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><title>PenguinHarness</title>`))
	}))
	defer server.Close()

	ok, err := probeHarness(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("probeHarness returned error: %v", err)
	}
	if !ok {
		t.Fatal("probeHarness did not recognize PenguinHarness page")
	}
}

func TestProbeHarnessRejectsUnrelatedHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><title>Other App</title>`))
	}))
	defer server.Close()

	ok, err := probeHarness(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("probeHarness returned error: %v", err)
	}
	if ok {
		t.Fatal("probeHarness accepted unrelated page")
	}
}

func TestReadPortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server-port")
	if err := os.WriteFile(path, []byte("64222\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	port, err := readPortFile(path)
	if err != nil {
		t.Fatalf("readPortFile returned error: %v", err)
	}
	if port != 64222 {
		t.Fatalf("port = %d, want 64222", port)
	}
}
