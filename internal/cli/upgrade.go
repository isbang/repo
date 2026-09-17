package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/selfupdate"
	"github.com/isbang/repo/internal/semver"
	"github.com/isbang/repo/internal/update"
)

func (a *app) newUpgradeCmd() *cobra.Command {
	var yes, force bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Replace this binary with the latest release",
		Long: `Download the latest release built for this platform and replace the running
binary with it.

The archive is checked against the checksums published with the release before
anything is installed, and the binary is swapped in with a rename, so an
interrupted upgrade leaves the working copy in place. A binary you cannot write
to — one a package manager installed, say — is left alone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runUpgrade(cmd.Context(), yes, force)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "install without asking")
	cmd.Flags().BoolVar(&force, "force", false, "install even when the running version is not older")
	return cmd
}

func (a *app) runUpgrade(ctx context.Context, yes, force bool) error {
	current := a.versionString()

	rel, err := a.latestRelease(ctx)
	if err != nil {
		return err
	}

	if !force && !semver.Newer(rel.TagName, current) {
		if semver.Valid(current) {
			fmt.Fprintf(os.Stderr, "already on the latest release (%s)\n", current)
		} else {
			fmt.Fprintf(os.Stderr, "this build carries no release version to compare; the latest release is %s (use --force to install it)\n", rel.TagName)
		}
		a.notifyUpdate = false
		return nil
	}
	return a.install(ctx, rel, current, yes)
}

// install replaces the running binary, asking first unless confirmed already.
func (a *app) install(ctx context.Context, rel ghapi.Release, current string, confirmed bool) error {
	target, err := selfupdate.Target()
	if err != nil {
		return err
	}
	// Check before asking: there is no point confirming an upgrade that cannot
	// be written anyway.
	if err := selfupdate.Writable(target); err != nil {
		return fmt.Errorf("%w; reinstall it the way it was installed", err)
	}

	if !confirmed {
		ok, err := confirm(fmt.Sprintf("update %s → %s in %s?", current, rel.TagName, target))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "not updated")
			a.notifyUpdate = false
			return nil
		}
	}

	if err := selfupdate.Apply(ctx, rel, target, downloadProgress()); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "updated to %s (%s)\n", rel.TagName, target)

	// Record what is now installed, so nothing else re-announces this release.
	_ = update.Save(&update.State{CheckedAt: time.Now(), Latest: rel.TagName, URL: rel.HTMLURL})
	a.notifyUpdate = false
	return nil
}

// offerUpdate reports a newer release once the command is done and, on a
// terminal, offers to install it there and then.
func (a *app) offerUpdate() {
	if !a.notifyUpdate {
		return
	}
	current := a.versionString()
	msg := update.Notice(current)
	if msg == "" {
		return
	}
	fmt.Fprintln(os.Stderr, msg)

	if !a.cfg.UpdatePrompts() || !interactive() {
		return
	}
	ok, err := confirm("update now?")
	if err != nil || !ok {
		return
	}

	// The command is over, so this gets its own cancellable context.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rel, err := a.latestRelease(ctx)
	if err == nil {
		err = a.install(ctx, rel, current, true)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "repo: upgrade failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "repo: install it by hand from %s\n", update.ReleasesURL)
	}
}

func (a *app) latestRelease(ctx context.Context) (ghapi.Release, error) {
	client := ghapi.New(a.releaseToken(ctx), update.Host)

	rel, err := client.LatestRelease(ctx, update.Owner, update.Name)
	if errors.Is(err, ghapi.ErrNoRelease) {
		return rel, fmt.Errorf("no release has been published on %s/%s/%s yet", update.Host, update.Owner, update.Name)
	}
	return rel, err
}

// confirm asks a yes/no question on stderr, defaulting to no. It is only
// called when stdin is a terminal.
func confirm(question string) (bool, error) {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// downloadProgress draws the download percentage, on a terminal only.
func downloadProgress() func(done, total int64) {
	if !isTTY(os.Stderr) {
		return nil
	}
	last := -1
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct == last {
			return
		}
		last = pct
		fmt.Fprintf(os.Stderr, "\rrepo: downloading… %d%%", pct)
		if done >= total {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	}
}
