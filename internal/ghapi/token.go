package ghapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoToken is returned when no GitHub token could be discovered.
var ErrNoToken = errors.New("no GitHub token found")

// Token discovers a GitHub token for host, returning the token and a short
// description of where it came from. The lookup order is:
//
//  1. $REPO_GITHUB_TOKEN, $GH_TOKEN, $GITHUB_TOKEN
//  2. `gh auth token --hostname <host>` (works with gh's keyring storage)
//  3. the oauth_token entry in gh's hosts.yml
func Token(ctx context.Context, host string) (token, source string, err error) {
	for _, name := range []string{"REPO_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, "$" + name, nil
		}
	}
	if v, err := ghCLIToken(ctx, host); err == nil && v != "" {
		return v, "gh auth token", nil
	}
	if v, path, err := hostsFileToken(host); err == nil && v != "" {
		return v, path, nil
	}
	return "", "", fmt.Errorf("%w: run `gh auth login` or set $GITHUB_TOKEN", ErrNoToken)
}

func ghCLIToken(ctx context.Context, host string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", host).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// hostsFileToken scans gh's hosts.yml for the oauth_token of host. gh stores
// tokens in the system keyring when it can, so this is only a fallback for
// installs configured with GH_CONFIG_DIR or an insecure token store.
func hostsFileToken(host string) (token, path string, err error) {
	dir := os.Getenv("GH_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(configHome(), "gh")
	}
	path = filepath.Join(dir, "hosts.yml")

	f, err := os.Open(path)
	if err != nil {
		return "", path, err
	}
	defer f.Close()

	inHost := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A top-level key (no leading whitespace) starts a new host block.
		if line == trimmed && strings.HasSuffix(trimmed, ":") {
			inHost = strings.TrimSuffix(trimmed, ":") == host
			continue
		}
		if inHost {
			if v, ok := strings.CutPrefix(trimmed, "oauth_token:"); ok {
				return strings.Trim(strings.TrimSpace(v), `"'`), path, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", path, err
	}
	return "", path, fmt.Errorf("no oauth_token for %s in %s", host, path)
}

func configHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return os.TempDir()
	}
	return filepath.Join(home, ".config")
}
