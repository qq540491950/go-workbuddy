package main

import (
	"os"
	"path/filepath"
)

// osUserConfigDir resolves <user config dir>/<name>, creating it if needed.
func osUserConfigDir(name string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
