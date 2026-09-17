package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/cache"
	"github.com/isbang/repo/internal/humanize"
	"github.com/isbang/repo/internal/refresh"
	"github.com/isbang/repo/internal/xdg"
)

func (a *app) newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect or clear the repository cache",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the cache file path",
			Args:  cobra.NoArgs,
			Run: func(*cobra.Command, []string) {
				fmt.Println(cache.Path())
			},
		},
		a.newCacheInfoCmd(),
		a.newCacheClearCmd(),
	)
	return cmd
}

func (a *app) newCacheInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show cache location, age and contents",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			defer w.Flush()

			fmt.Fprintf(w, "cache file\t%s\n", cache.Path())
			fmt.Fprintf(w, "config file\t%s\n", configLocation(a))
			fmt.Fprintf(w, "state dir\t%s\n", xdg.StateDir())

			file, err := cache.Load()
			if errors.Is(err, cache.ErrNotExist) {
				fmt.Fprintf(w, "status\tempty (run `repo sync`)\n")
				return nil
			}
			if err != nil {
				return err
			}

			_, size, _ := cache.Stat()
			var private, forks, archived int
			for _, r := range file.Repos {
				if r.Private {
					private++
				}
				if r.Fork {
					forks++
				}
				if r.Archived {
					archived++
				}
			}

			fmt.Fprintf(w, "host\t%s\n", file.Host)
			fmt.Fprintf(w, "user\t%s\n", file.User)
			fmt.Fprintf(w, "repositories\t%d (%d private, %d forks, %d archived)\n",
				len(file.Repos), private, forks, archived)
			fmt.Fprintf(w, "size\t%s\n", humanize.Bytes(size))
			fmt.Fprintf(w, "fetched\t%s (%s)\n", file.FetchedAt.Format("2006-01-02 15:04:05"), humanize.Ago(file.FetchedAt))
			fmt.Fprintf(w, "refreshing\t%t\n", refresh.InProgress())
			fmt.Fprintf(w, "update check\t%s\n", a.updateStatus())
			if log := strings.TrimSpace(refresh.LastError()); log != "" {
				fmt.Fprintf(w, "refresh log\t%s\n", refresh.LogPath())
				for _, line := range lastLines(log, 3) {
					fmt.Fprintf(w, "\t%s\n", line)
				}
			}
			return nil
		},
	}
}

func (a *app) newCacheClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Delete the cache file",
		Long: `Delete the cache file. Nothing else is touched: the next command repopulates it
from GitHub.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			path := cache.Path()
			count := 0
			if file, err := cache.Load(); err == nil {
				count = len(file.Repos)
			}
			removed, err := cache.Remove()
			if err != nil {
				return err
			}
			if !removed {
				fmt.Fprintf(os.Stderr, "no cache to remove (%s)\n", path)
				return nil
			}
			fmt.Fprintf(os.Stderr, "removed %s (%d repositories)\n", path, count)
			return nil
		},
	}
}

func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
