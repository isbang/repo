// Package gitx shells out to git for cloning.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Available reports whether a git binary is on PATH.
func Available() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git not found in PATH")
	}
	return nil
}

// Options describes one clone.
type Options struct {
	URL  string
	Dest string
	// Args are extra arguments inserted before the URL (e.g. --depth=1).
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
	// NoPrompt makes git fail instead of asking for credentials, for clones
	// running in the background with nobody watching the terminal.
	NoPrompt bool
}

// Clone runs `git clone` into opts.Dest.
func Clone(ctx context.Context, opts Options) error {
	if err := Available(); err != nil {
		return err
	}
	args := append([]string{"clone"}, opts.Args...)
	args = append(args, "--", opts.URL, opts.Dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	cmd.Stdin = nil
	if opts.NoPrompt {
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

// CheckDest reports whether dest can be cloned into. An existing empty
// directory is fine — that is what git itself accepts.
func CheckDest(dest string) error {
	st, err := os.Stat(dest)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", dest)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		return fmt.Errorf("%s is already a git repository", dest)
	}
	return fmt.Errorf("%s already exists and is not empty", dest)
}
