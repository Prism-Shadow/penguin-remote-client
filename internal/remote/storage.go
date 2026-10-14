package remote

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SavePrivateJSON uses the same restricted, atomic storage as the device config.
func SavePrivateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicConfigWrite(path, append(data, '\n'))
}

func atomicConfigWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".remote-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = restrictFile(name); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
