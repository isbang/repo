package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/cache"
	"github.com/isbang/repo/internal/refresh"
)

func (a *app) newSyncCmd() *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Refresh the repository cache now, in the foreground",
		Long: `Fetch the repository list from GitHub and rewrite the cache.

Every other command already refreshes the cache in the background; use sync when
you want to wait for a fresh list, or to see why a refresh is failing.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "only report errors")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		start := time.Now()
		progress := progressReporter()
		if quiet {
			progress = nil
		}

		file, err := refresh.Run(cmd.Context(), a.cfg, progress)
		if errors.Is(err, refresh.ErrLocked) {
			return fmt.Errorf("%w (a background refresh is running; try again in a moment)", err)
		}
		if err != nil {
			return err
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "cached %d repositories for %s in %s → %s\n",
				len(file.Repos), file.User, time.Since(start).Round(100*time.Millisecond), cache.Path())
		}
		return nil
	}
	return cmd
}

func (a *app) newRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:    refresh.CommandName,
		Short:  "Internal: refresh the cache (used by the background process)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			start := time.Now()
			file, err := refresh.Run(cmd.Context(), a.cfg, nil)
			// The update check rides along with this process rather than
			// running in anyone's foreground. It does not need the cache, so a
			// failed refresh does not skip it.
			a.checkForUpdate(cmd.Context())
			if err != nil {
				if errors.Is(err, refresh.ErrLocked) {
					// Another refresh is doing the work; nothing to report.
					return nil
				}
				refresh.Logf("refresh failed: %v", err)
				return err
			}
			refresh.Logf("refreshed %d repositories in %s", len(file.Repos), time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
}
