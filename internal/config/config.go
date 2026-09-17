// Package config loads the optional user configuration file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/isbang/repo/internal/xdg"
)

// Protocols supported for cloning.
const (
	ProtocolHTTPS = "https"
	ProtocolSSH   = "ssh"
)

// DefaultAffiliation mirrors the GitHub API default: everything you can reach.
const DefaultAffiliation = "owner,collaborator,organization_member"

// DefaultCloneConcurrency is how many queued clones run in parallel. Clones are
// network-bound, so a couple at a time is plenty.
const DefaultCloneConcurrency = 2

// DefaultUpdateCheckInterval is how often the background process asks GitHub
// whether a newer release of this CLI exists, in seconds.
const DefaultUpdateCheckInterval = 24 * 60 * 60

// Config is the user configuration, read from $XDG_CONFIG_HOME/repo/config.json.
// Every field is optional; zero values fall back to the defaults below.
type Config struct {
	// Host is the GitHub host to talk to. github.com or a GitHub Enterprise host.
	Host string `json:"host"`
	// Protocol selects the clone URL: "https" or "ssh".
	Protocol string `json:"protocol"`
	// Affiliation is the GitHub API affiliation filter for the repository list.
	Affiliation string `json:"affiliation"`
	// CloneDir is the parent directory clones land in. Empty means the current
	// working directory.
	CloneDir string `json:"clone_dir"`
	// IncludeForks and IncludeArchived are the default visibility of forks and
	// archived repositories in listings.
	IncludeForks    *bool `json:"include_forks"`
	IncludeArchived *bool `json:"include_archived"`
	// RefreshMinInterval throttles the background refresh: a refresh is skipped
	// when the cache is younger than this many seconds. 0 refreshes every run.
	RefreshMinInterval int `json:"refresh_min_interval_seconds"`
	// CloneConcurrency is how many queued clones run at once in the picker.
	CloneConcurrency int `json:"clone_concurrency"`
	// GitArgs are extra arguments passed to every `git clone`.
	GitArgs []string `json:"git_args"`
	// UpdateCheck enables the "a new version is available" notice.
	UpdateCheck *bool `json:"update_check"`
	// UpdateCheckInterval throttles that check: GitHub is asked at most once
	// per this many seconds.
	UpdateCheckInterval int `json:"update_check_interval_seconds"`
	// UpdatePrompt offers to install a new release when one is announced. The
	// offer only appears on a terminal, and always defaults to no.
	UpdatePrompt *bool `json:"update_prompt"`

	// path records where this config was read from (empty when defaulted).
	path string
}

// Default returns the configuration used when no file exists.
func Default() *Config {
	t := true
	return &Config{
		Host:               "github.com",
		Protocol:           ProtocolHTTPS,
		Affiliation:        DefaultAffiliation,
		IncludeForks:       &t,
		IncludeArchived:    &t,
		RefreshMinInterval: 0,
		CloneConcurrency:   DefaultCloneConcurrency,

		UpdateCheck:         &t,
		UpdateCheckInterval: DefaultUpdateCheckInterval,
		UpdatePrompt:        &t,
	}
}

// Path is the config file location. $REPO_CONFIG_FILE overrides it.
func Path() string {
	if p := os.Getenv("REPO_CONFIG_FILE"); p != "" {
		return p
	}
	return filepath.Join(xdg.ConfigDir(), "config.json")
}

// Load reads the config file, applies defaults and environment overrides, and
// validates the result. A missing file is not an error.
func Load() (*Config, error) {
	cfg := Default()
	path := Path()

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		cfg.path = path
	case errors.Is(err, os.ErrNotExist):
		// Defaults are fine.
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg.applyEnv()
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("REPO_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("REPO_PROTOCOL"); v != "" {
		c.Protocol = v
	}
	if v := os.Getenv("REPO_AFFILIATION"); v != "" {
		c.Affiliation = v
	}
	if v := os.Getenv("REPO_CLONE_DIR"); v != "" {
		c.CloneDir = v
	}
	if v := os.Getenv("REPO_REFRESH_MIN_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.RefreshMinInterval = n
		}
	}
	if v := os.Getenv("REPO_CLONE_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.CloneConcurrency = n
		}
	}
	if v := os.Getenv("REPO_UPDATE_CHECK_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.UpdateCheckInterval = n
		}
	}
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.Host == "" {
		c.Host = d.Host
	}
	if c.Protocol == "" {
		c.Protocol = d.Protocol
	}
	if c.Affiliation == "" {
		c.Affiliation = d.Affiliation
	}
	if c.IncludeForks == nil {
		c.IncludeForks = d.IncludeForks
	}
	if c.IncludeArchived == nil {
		c.IncludeArchived = d.IncludeArchived
	}
	if c.CloneConcurrency < 1 {
		c.CloneConcurrency = d.CloneConcurrency
	}
	if c.UpdateCheck == nil {
		c.UpdateCheck = d.UpdateCheck
	}
	if c.UpdatePrompt == nil {
		c.UpdatePrompt = d.UpdatePrompt
	}
	if c.UpdateCheckInterval < 0 {
		c.UpdateCheckInterval = d.UpdateCheckInterval
	}
	if c.CloneDir != "" {
		c.CloneDir = expandHome(c.CloneDir)
	}
}

// Validate reports configuration values the CLI cannot work with.
func (c *Config) Validate() error {
	switch c.Protocol {
	case ProtocolHTTPS, ProtocolSSH:
	default:
		return fmt.Errorf("invalid protocol %q: want %q or %q", c.Protocol, ProtocolHTTPS, ProtocolSSH)
	}
	if strings.Contains(c.Host, "/") {
		return fmt.Errorf("invalid host %q: want a bare hostname", c.Host)
	}
	return nil
}

// Forks reports whether forks are listed by default.
func (c *Config) Forks() bool { return c.IncludeForks == nil || *c.IncludeForks }

// Archived reports whether archived repositories are listed by default.
func (c *Config) Archived() bool { return c.IncludeArchived == nil || *c.IncludeArchived }

// UpdateChecks reports whether the update notice is switched on.
func (c *Config) UpdateChecks() bool { return c.UpdateCheck == nil || *c.UpdateCheck }

// UpdatePrompts reports whether a new release is offered for installation
// rather than only announced.
func (c *Config) UpdatePrompts() bool {
	if os.Getenv("REPO_NO_UPDATE_PROMPT") != "" {
		return false
	}
	return c.UpdatePrompt == nil || *c.UpdatePrompt
}

// UpdateInterval is how long an update check result is trusted. 0 in the config
// means "check on every background refresh".
func (c *Config) UpdateInterval() time.Duration {
	return time.Duration(c.UpdateCheckInterval) * time.Second
}

// SourcePath returns the file this config was loaded from, or "" for defaults.
func (c *Config) SourcePath() string { return c.path }

// WriteDefault writes a commented-by-example config file, unless one exists.
func WriteDefault() (string, error) {
	path := Path()
	if _, err := os.Stat(path); err == nil {
		return path, os.ErrExist
	}
	if _, err := xdg.Ensure(filepath.Dir(path)); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}
