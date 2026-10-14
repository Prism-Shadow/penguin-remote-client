//go:build !windows

package remote

import "os"

func restrictFile(path string) error { return os.Chmod(path, 0600) }
