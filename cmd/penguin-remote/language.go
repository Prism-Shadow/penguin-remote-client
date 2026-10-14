package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func languagePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "ui-language")
}

func loadLanguage(configPath string) string {
	data, err := os.ReadFile(languagePath(configPath))
	if err == nil && strings.TrimSpace(string(data)) == "en" {
		return "en"
	}
	return "zh"
}

func (a *appServer) handleLanguage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON is required", http.StatusUnsupportedMediaType)
		return
	}
	var request struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64)).Decode(&request); err != nil ||
		(request.Language != "zh" && request.Language != "en") {
		http.Error(w, "language must be zh or en", http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.WriteFile(languagePath(a.configPath), []byte(request.Language+"\n"), 0o600); err != nil {
		http.Error(w, "could not save language preference", http.StatusInternalServerError)
		return
	}
	a.language = request.Language
	writeJSON(w, map[string]string{"language": request.Language})
}
