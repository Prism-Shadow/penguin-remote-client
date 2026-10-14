package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func themePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "ui-theme")
}

func loadTheme(configPath string) string {
	data, err := os.ReadFile(themePath(configPath))
	if err == nil {
		switch strings.TrimSpace(string(data)) {
		case "light":
			return "light"
		case "dark":
			return "dark"
		}
	}
	return "system"
}

func (a *appServer) handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON is required", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64)).Decode(&request); err != nil ||
		(request.Theme != "system" && request.Theme != "light" && request.Theme != "dark") {
		http.Error(w, "theme must be system, light or dark", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(themePath(a.configPath), []byte(request.Theme+"\n"), 0o600); err != nil {
		http.Error(w, "could not save theme preference", http.StatusInternalServerError)
		return
	}
	a.theme = request.Theme
	writeJSON(w, map[string]string{"theme": request.Theme})
}
