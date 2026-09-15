// Package refresh updates the repository cache, either in the foreground or as
// a detached background process started on every CLI run.
package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/isbang/repo/internal/cache"
	"github.com/isbang/repo/internal/config"
	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/xdg"
)

// CommandName is the hidden subcommand the background process runs.
const CommandName = "__refresh"

// EnvDisable is set in the child's environment so it never spawns a refresh of
// its own, and can be set by the user to opt out entirely.
const EnvDisable = "REPO_NO_REFRESH"

const (
	// staleMarker is how long a marker file is trusted before being ignored.
	staleMarker = 3 * time.Minute
	// maxLogSize caps the background log; it is truncated past this size.
	maxLogSize = 512 << 10
)

// ErrLocked means another refresh is already running.
var ErrLocked = errors.New("another refresh is already running")

func statePath(name string) string { return filepath.Join(xdg.StateDir(), name) }

// LockPath, MarkerPath and LogPath are exposed for `repo cache info`.
func LockPath() string   { return statePath("refresh.lock") }
func MarkerPath() string { return statePath("refresh.state") }
func LogPath() string    { return statePath("refresh.log") }

// Disabled reports whether the background refresh is switched off.
func Disabled() bool { return os.Getenv(EnvDisable) != "" }

// Due reports whether the cache is old enough to warrant a refresh.
func Due(cfg *config.Config) bool {
	if cfg.RefreshMinInterval <= 0 {
		return true
	}
	mod, _, err := cache.Stat()
	if err != nil {
		return true
	}
	return time.Since(mod) >= time.Duration(cfg.RefreshMinInterval)*time.Second
}

// Spawn starts a detached background refresh and returns immediately. It is a
// no-op when refreshing is disabled, not yet due, or already running.
func Spawn() error {
	if Disabled() || InProgress() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if _, err := xdg.Ensure(xdg.StateDir()); err != nil {
		return err
	}

	logFile, err := openLog()
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, CommandName)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), EnvDisable+"=1")
	cmd.SysProcAttr = detachAttr()

	if err := cmd.Start(); err != nil {
		return err
	}
	// Detach: we never wait for it, the init process reaps it.
	return cmd.Process.Release()
}

func openLog() (*os.File, error) {
	path := LogPath()
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		_ = os.Truncate(path, 0)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// Run refreshes the cache in the current process. It returns ErrLocked when
// another refresh holds the lock.
func Run(ctx context.Context, cfg *config.Config, progress func(done, total int)) (*cache.File, error) {
	if _, err := xdg.Ensure(xdg.StateDir()); err != nil {
		return nil, err
	}
	release, ok, err := tryLock(LockPath())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLocked
	}
	defer release()

	writeMarker()
	defer os.Remove(MarkerPath())

	token, _, err := ghapi.Token(ctx, cfg.Host)
	if err != nil {
		return nil, err
	}
	client := ghapi.New(token, cfg.Host)

	user, err := client.Viewer(ctx)
	if err != nil {
		return nil, err
	}
	repos, err := client.ListRepos(ctx, cfg.Affiliation, progress)
	if err != nil {
		return nil, err
	}

	file := &cache.File{
		Host:      cfg.Host,
		User:      user,
		FetchedAt: time.Now(),
		Repos:     repos,
	}
	if err := cache.Save(file); err != nil {
		return nil, err
	}
	return file, nil
}

type marker struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

func writeMarker() {
	b, err := json.Marshal(marker{PID: os.Getpid(), StartedAt: time.Now()})
	if err != nil {
		return
	}
	_ = os.WriteFile(MarkerPath(), b, 0o600)
}

// InProgress reports whether a refresh started recently and has not finished.
// It reads a marker file rather than probing the lock, so that checking never
// interferes with a refresher trying to acquire it.
func InProgress() bool {
	b, err := os.ReadFile(MarkerPath())
	if err != nil {
		return false
	}
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	return time.Since(m.StartedAt) < staleMarker
}

// LastError returns the tail of the background refresh log, for diagnostics.
func LastError() string {
	b, err := os.ReadFile(LogPath())
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > 2<<10 {
		b = b[len(b)-(2<<10):]
	}
	return string(b)
}

// Logf writes a timestamped line to stdout, which the background process has
// pointed at the refresh log.
func Logf(format string, args ...any) {
	fmt.Printf("%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
