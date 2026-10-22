package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var updateVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func updateSeenPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "ui-update-seen")
}

func loadUpdateSeen(configPath string) string {
	data, err := os.ReadFile(updateSeenPath(configPath))
	version := strings.TrimSpace(string(data))
	if err != nil || len(version) > 48 || !updateVersionPattern.MatchString(version) {
		return ""
	}
	return version
}

func (a *appServer) handleUpdateSeen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Host != strings.TrimPrefix(a.baseURL, "http://") || r.Header.Get("Origin") != a.baseURL || r.Header.Get("X-Penguin-Update") != "1" {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON is required", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128)).Decode(&request); err != nil || len(request.Version) > 48 || !updateVersionPattern.MatchString(request.Version) {
		http.Error(w, "invalid update version", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(updateSeenPath(a.configPath), []byte(request.Version+"\n"), 0o600); err != nil {
		http.Error(w, "could not save update preference", http.StatusInternalServerError)
		return
	}
	a.updateSeenVersion = request.Version
	writeJSON(w, map[string]string{"seenVersion": request.Version})
}
