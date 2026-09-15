// Package xdg resolves the XDG Base Directory locations this CLI stores data in.
//
// See https://specification.freedesktop.org/basedir-spec/latest/. Relative
// values in the environment are ignored, as the spec requires.
package xdg

import (
	"os"
	"path/filepath"
)

// App is the directory name appended to each XDG base directory.
const App = "repo"

func base(envVar, fallback string) string {
	if v := os.Getenv(envVar); filepath.IsAbs(v) {
		return filepath.Join(v, App)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), App)
	}
	return filepath.Join(home, filepath.FromSlash(fallback), App)
}

// CacheDir is where the repository list cache lives ($XDG_CACHE_HOME/repo).
func CacheDir() string { return base("XDG_CACHE_HOME", ".cache") }

// ConfigDir is where config.json lives ($XDG_CONFIG_HOME/repo).
func ConfigDir() string { return base("XDG_CONFIG_HOME", ".config") }

// StateDir is where the refresh lock, marker and log live ($XDG_STATE_HOME/repo).
func StateDir() string { return base("XDG_STATE_HOME", ".local/state") }

// Ensure creates dir (mode 0700, like the rest of the XDG dirs) and returns it.
func Ensure(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
