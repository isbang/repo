// Package history records which repositories were cloned, so the picker can
// float the ones you reach for most to the top.
package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/isbang/repo/internal/xdg"
)

// Version is bumped when the file layout changes; an older file is ignored.
const Version = 1

const (
	// CountWindow and CountEvents define what "recent" means for ranking: the
	// most recent CountEvents clones that happened within CountWindow.
	CountWindow = 30 * 24 * time.Hour
	CountEvents = 100

	// retention bounds the file: older events are dropped on write.
	retention = 180 * 24 * time.Hour
	maxEvents = 500
)

// Event is one clone.
type Event struct {
	FullName string    `json:"full_name"`
	At       time.Time `json:"at"`
}

// File is the on-disk history, oldest event first.
type File struct {
	Version int     `json:"version"`
	Events  []Event `json:"events"`
}

// mu serialises the read-modify-write cycle within this process; the queue's
// workers finish clones concurrently.
var mu sync.Mutex

// Path is the history file location. $REPO_HISTORY_FILE overrides it.
//
// It lives in the XDG state directory, where the spec puts history-like data
// that should survive restarts but is not precious.
func Path() string {
	if p := os.Getenv("REPO_HISTORY_FILE"); p != "" {
		return p
	}
	return filepath.Join(xdg.StateDir(), "history.json")
}

// Load reads the history. A missing, empty, corrupt or outdated file reads as
// empty history rather than an error: it is only a ranking hint.
func Load() *File {
	b, err := os.ReadFile(Path())
	if err != nil {
		return &File{Version: Version}
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil || f.Version != Version {
		return &File{Version: Version}
	}
	return &f
}

// Counts tallies the recent clones per repository: the most recent CountEvents
// events that are younger than CountWindow.
func (f *File) Counts() map[string]int {
	cutoff := time.Now().Add(-CountWindow)
	counts := map[string]int{}

	seen := 0
	for i := len(f.Events) - 1; i >= 0 && seen < CountEvents; i-- {
		e := f.Events[i]
		if e.At.Before(cutoff) {
			// Events are appended in order, so everything older follows.
			break
		}
		counts[e.FullName]++
		seen++
	}
	return counts
}

// Counts is Load followed by File.Counts.
func Counts() map[string]int { return Load().Counts() }

// Record appends a clone of fullName and prunes the file. A failure to write is
// returned but is never fatal to the caller: history only affects ordering.
func Record(fullName string) error {
	if fullName == "" {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()

	f := Load()
	f.Version = Version
	f.Events = append(f.Events, Event{FullName: fullName, At: time.Now()})
	f.prune()
	return f.save()
}

func (f *File) prune() {
	cutoff := time.Now().Add(-retention)
	kept := f.Events[:0]
	for _, e := range f.Events {
		if e.At.After(cutoff) {
			kept = append(kept, e)
		}
	}
	f.Events = kept
	if extra := len(f.Events) - maxEvents; extra > 0 {
		f.Events = f.Events[extra:]
	}
}

// save writes the file atomically so a concurrent reader never sees a partial one.
func (f *File) save() error {
	path := Path()
	dir := filepath.Dir(path)
	if _, err := xdg.Ensure(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".history-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds

	if err := json.NewEncoder(tmp).Encode(f); err != nil {
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

// Remove deletes the history file. A missing file is not an error.
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
