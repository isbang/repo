// Package cache stores the repository list in the XDG cache directory.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/xdg"
)

// Version is bumped whenever the on-disk layout changes; an older file is
// treated as a cache miss.
const Version = 1

// ErrNotExist means there is no usable cache yet.
var ErrNotExist = errors.New("no repository cache")

// File is the cached repository list.
type File struct {
	Version   int          `json:"version"`
	Host      string       `json:"host"`
	User      string       `json:"user"`
	FetchedAt time.Time    `json:"fetched_at"`
	Repos     []ghapi.Repo `json:"repos"`
}

// Age reports how long ago the cache was written.
func (f *File) Age() time.Duration { return time.Since(f.FetchedAt) }

// Path is the cache file location. $REPO_CACHE_FILE overrides it.
func Path() string {
	if p := os.Getenv("REPO_CACHE_FILE"); p != "" {
		return p
	}
	return filepath.Join(xdg.CacheDir(), "repos.json")
}

// Load reads the cache. It returns ErrNotExist when the file is missing, empty
// or written by an incompatible version.
func Load() (*File, error) {
	path := Path()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotExist
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(b) == 0 {
		return nil, ErrNotExist
	}

	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		// A corrupt cache is recoverable: refetch instead of failing.
		return nil, ErrNotExist
	}
	if f.Version != Version {
		return nil, ErrNotExist
	}
	return &f, nil
}

// Save writes the cache atomically so a reader never sees a partial file.
func Save(f *File) error {
	f.Version = Version
	path := Path()
	dir := filepath.Dir(path)
	if _, err := xdg.Ensure(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".repos-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "")
	if err := enc.Encode(f); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Stat returns the cache file's modification time and size.
func Stat() (modTime time.Time, size int64, err error) {
	st, err := os.Stat(Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return time.Time{}, 0, ErrNotExist
		}
		return time.Time{}, 0, err
	}
	return st.ModTime(), st.Size(), nil
}

// Remove deletes the cache file. A missing file is not an error.
func Remove() (removed bool, err error) {
	err = os.Remove(Path())
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
