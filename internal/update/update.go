// Package update tells you when a newer release of this CLI is published.
//
// The check never runs in the foreground: the detached refresh process asks
// GitHub at most once a day and writes the answer to the state directory, and
// commands only read that file. A command therefore costs no extra network
// round trip, and GitHub being slow or down can never delay one.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/semver"
	"github.com/isbang/repo/internal/xdg"
)

// Version is bumped when the file layout changes; an older file is ignored.
const Version = 1

const (
	// Owner and Name identify the repository releases are published from. They
	// are fixed: even against GitHub Enterprise, this CLI ships from github.com.
	Owner = "isbang"
	Name  = "repo"

	// Host is the GitHub host releases are looked up on.
	Host = "github.com"

	// ReleasesURL is where the notice sends you when a release carries no URL.
	ReleasesURL = "https://github.com/" + Owner + "/" + Name + "/releases/latest"

	// EnvDisable switches the check off entirely.
	EnvDisable = "REPO_NO_UPDATE_CHECK"

	// DefaultInterval is how long a check result is trusted before asking again.
	DefaultInterval = 24 * time.Hour
)

// State is what the last check found.
type State struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	// Latest is the newest published tag, empty when nothing is published yet.
	Latest string `json:"latest,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Path is the state file location. $REPO_UPDATE_FILE overrides it.
func Path() string {
	if p := os.Getenv("REPO_UPDATE_FILE"); p != "" {
		return p
	}
	return filepath.Join(xdg.StateDir(), "update.json")
}

// Disabled reports whether the user switched the check off in the environment.
func Disabled() bool { return os.Getenv(EnvDisable) != "" }

// Load reads the last check. A missing, empty, corrupt or outdated file reads
// as "never checked" rather than an error: it is only a notice.
func Load() *State {
	b, err := os.ReadFile(Path())
	if err != nil || len(b) == 0 {
		return nil
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil || s.Version != Version {
		return nil
	}
	return &s
}

// Save writes the state atomically, so a reader never sees a partial file.
func Save(s *State) error {
	s.Version = Version
	path := Path()
	dir := filepath.Dir(path)
	if _, err := xdg.Ensure(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".update-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds

	if err := json.NewEncoder(tmp).Encode(s); err != nil {
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

// Due reports whether the last check is older than interval.
func Due(interval time.Duration) bool {
	s := Load()
	if s == nil {
		return true
	}
	return time.Since(s.CheckedAt) >= interval
}

// Check asks GitHub for the newest release and records the answer, including
// the answer "nothing published yet" — so an unreleased repository is asked
// about once a day, not once a command.
//
// token may be empty; it only raises the rate limit.
func Check(ctx context.Context, token string) (*State, error) {
	return check(ctx, ghapi.New(token, Host))
}

func check(ctx context.Context, client *ghapi.Client) (*State, error) {
	s := &State{CheckedAt: time.Now()}

	rel, err := client.LatestRelease(ctx, Owner, Name)
	switch {
	case errors.Is(err, ghapi.ErrNoRelease):
	case err != nil:
		return nil, err
	default:
		s.Latest, s.URL = rel.TagName, rel.HTMLURL
	}
	if err := Save(s); err != nil {
		return nil, err
	}
	return s, nil
}

// Notice is the line to print for current, or "" when there is nothing to say:
// no check has landed yet, the release is not newer, or this build carries no
// comparable version (a `make build` of a dirty tree, say).
func Notice(current string) string {
	if Disabled() {
		return ""
	}
	return Load().NoticeFor(current)
}

// NoticeFor is Notice against an already-loaded state.
func (s *State) NoticeFor(current string) string {
	if !s.Newer(current) {
		return ""
	}
	return fmt.Sprintf("repo: %s is available (you have %s)\n%s", s.Latest, current, s.releaseURL())
}

// Newer reports whether the recorded release is newer than current.
func (s *State) Newer(current string) bool {
	if s == nil {
		return false
	}
	return semver.Newer(s.Latest, current)
}

func (s *State) releaseURL() string {
	if s != nil && s.URL != "" {
		return s.URL
	}
	return ReleasesURL
}
