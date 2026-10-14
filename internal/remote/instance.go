package remote

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClientInstanceIDFile is the file, next to the config file, that holds the id
// of this installation. It lives in Remote's own config directory, so two
// installations - two OS users, or two data directories - never share one.
const ClientInstanceIDFile = "client-instance-id"

// clientInstanceIDBytes is the entropy of a generated id. It is sized like a
// session secret because Penguin Go may use the id to tell installations apart
// when it hands out concurrency slots.
const clientInstanceIDBytes = 32

// maxClientInstanceIDLength bounds what a hand-edited file may claim to be.
const maxClientInstanceIDLength = 128

// minClientInstanceIDLength is the shortest value still treated as a stored id.
// An id is 32 random bytes, so anything shorter cannot be one: it is a truncated
// file or something else entirely, and reusing it would give this installation a
// weak, guessable identity instead of a fresh one.
const minClientInstanceIDLength = 22

// ClientInstanceIDPath is where the installation id for a config file is kept.
func ClientInstanceIDPath(configPath string) (string, error) {
	path := strings.TrimSpace(configPath)
	if path == "" {
		return "", errors.New("config path is required to locate the client instance id")
	}
	return filepath.Join(filepath.Dir(path), ClientInstanceIDFile), nil
}

// LoadClientInstanceID returns the persistent id of this installation,
// generating and saving one the first time it is asked for. An existing file is
// reused across logins and restarts, so Penguin Go sees the same installation
// throughout.
//
// The id is drawn from the system random source and is deliberately unrelated
// to the machine: it is not derived from the host name, the user name or a MAC
// address, and it is not stable across installations. A file that is missing,
// empty or unreadable is replaced with a fresh id. Persisting that replacement
// is mandatory: continuing with a per-run id would make every restart look like
// a new installation to Penguin Go and consume a different concurrency slot.
func LoadClientInstanceID(configPath string) (string, error) {
	path, err := ClientInstanceIDPath(configPath)
	if err != nil {
		return "", err
	}
	if id, err := readClientInstanceID(path); err == nil {
		return id, nil
	}
	id, err := newClientInstanceID()
	if err != nil {
		return "", err
	}
	if err := writeClientInstanceID(path, id); err != nil {
		return "", fmt.Errorf("save client instance id: %w", err)
	}
	return id, nil
}

func readClientInstanceID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !validClientInstanceID(id) {
		return "", fmt.Errorf("client instance id file %s does not hold a usable id", path)
	}
	return id, nil
}

func writeClientInstanceID(path string, id string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(id+"\n"), 0o600)
}

func newClientInstanceID() (string, error) {
	raw := make([]byte, clientInstanceIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate client instance id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// validClientInstanceID accepts what this client generates and rejects anything
// else, so a corrupt or hand-edited file is replaced rather than forwarded.
func validClientInstanceID(id string) bool {
	if len(id) < minClientInstanceIDLength || len(id) > maxClientInstanceIDLength {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}
