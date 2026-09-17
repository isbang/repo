// Package cli wires the cobra command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/cache"
	"github.com/isbang/repo/internal/config"
	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/query"
	"github.com/isbang/repo/internal/refresh"
	"github.com/isbang/repo/internal/tui"
	"github.com/isbang/repo/internal/update"
)

// exitAborted is the conventional status for "user cancelled" (128 + SIGINT).
const exitAborted = 130

type app struct {
	version string
	cfg     *config.Config
	cfgErr  error

	noRefresh bool
	// notifyUpdate is decided in preRun and acted on once the command is done.
	notifyUpdate bool
}

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	a := &app{version: version}
	root := a.newRootCmd()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := root.ExecuteContext(ctx)
	if err == nil || errors.Is(err, tui.ErrAborted) {
		a.offerUpdate()
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, tui.ErrAborted), errors.Is(err, context.Canceled):
		return exitAborted
	default:
		fmt.Fprintf(os.Stderr, "repo: %v\n", err)
		return 1
	}
}

func (a *app) newRootCmd() *cobra.Command {
	// The config is loaded eagerly so flag defaults can come from it; a broken
	// config is reported in PersistentPreRunE rather than at construction.
	a.cfg, a.cfgErr = config.Load()
	if a.cfg == nil {
		a.cfg = config.Default()
	}

	root := &cobra.Command{
		Use:   "repo",
		Short: "Fuzzy-find and clone the GitHub repositories you can reach",
		Long: `repo lists every GitHub repository you can reach, lets you fuzzy-search them
and clones the one you pick.

The repository list is cached under $XDG_CACHE_HOME/repo and refreshed by a
detached background process on every run, so the picker opens instantly.

Running "repo" with no arguments opens the interactive picker; the selected
repository is cloned into the current directory.`,
		// Bare `repo` picks interactively. A stray argument is a typo rather than
		// a query: silently cloning something that fuzzy-matches `repo lst` would
		// be a nasty surprise.
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q; did you mean `repo clone %s`?",
					args[0], strings.Join(args, " "))
			}
			return nil
		},
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: a.preRun,
	}
	root.PersistentFlags().BoolVar(&a.noRefresh, "no-refresh", false,
		"skip the background cache refresh for this run")

	// Root behaves like `repo clone` with no query: pick, then clone.
	rootClone := a.registerCloneFlags(root)
	// Bare `repo` always picks interactively, so the query-resolution flags are
	// noise in its help.
	for _, name := range []string{"select", "first"} {
		_ = root.Flags().MarkHidden(name)
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return a.runClone(cmd, args, rootClone)
	}

	root.AddCommand(
		a.newCloneCmd(),
		a.newListCmd(),
		a.newSearchCmd(),
		a.newSyncCmd(),
		a.newCacheCmd(),
		a.newConfigCmd(),
		a.newVersionCmd(),
		a.newUpgradeCmd(),
		a.newRefreshCmd(),
	)
	return root
}

// preRun reports configuration problems and kicks off the background refresh.
func (a *app) preRun(cmd *cobra.Command, _ []string) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	a.notifyUpdate = a.wantUpdateNotice(cmd)
	if a.skipRefresh(cmd) || !refresh.Due(a.cfg) {
		return nil
	}
	if err := refresh.Spawn(); err != nil && os.Getenv("REPO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "repo: background refresh: %v\n", err)
	}
	return nil
}

// skipRefresh keeps the background refresh out of the way of commands that
// must stay fast (shell completion), that refresh themselves, or that are only
// inspecting local state.
func (a *app) skipRefresh(cmd *cobra.Command) bool {
	if a.noRefresh || refresh.Disabled() {
		return true
	}
	switch cmd.Name() {
	case refresh.CommandName, "sync", "help", "version", "upgrade", "completion",
		cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	if parent := cmd.Parent(); parent != nil {
		switch parent.Name() {
		case "completion", "cache", "config":
			return true
		}
	}
	return false
}

// wantUpdateNotice reports whether this command should end with the "a new
// version is available" line, and the offer to install it: only on a terminal,
// and never around output something else reads (completion scripts, the
// background refresh).
func (a *app) wantUpdateNotice(cmd *cobra.Command) bool {
	if update.Disabled() || !a.cfg.UpdateChecks() || !isTTY(os.Stderr) {
		return false
	}
	switch cmd.Name() {
	// upgrade says everything there is to say about releases itself.
	case refresh.CommandName, "upgrade", "completion",
		cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return false
	}
	if parent := cmd.Parent(); parent != nil && parent.Name() == "completion" {
		return false
	}
	return true
}

// filterFlags are the repository filters shared by several commands.
type filterFlags struct {
	owners   []string
	forks    bool
	archived bool
	private  bool
	public   bool
}

func (a *app) registerFilterFlags(cmd *cobra.Command) *filterFlags {
	f := &filterFlags{}
	flags := cmd.Flags()
	flags.StringSliceVar(&f.owners, "owner", nil, "only repositories of these owners (repeatable)")
	flags.BoolVar(&f.forks, "forks", a.cfg.Forks(), "include forks")
	flags.BoolVar(&f.archived, "archived", a.cfg.Archived(), "include archived repositories")
	flags.BoolVar(&f.private, "private", false, "only private repositories")
	flags.BoolVar(&f.public, "public", false, "only public repositories")
	_ = cmd.RegisterFlagCompletionFunc("owner", a.completeOwners)
	return f
}

func (f *filterFlags) filters() query.Filters {
	visibility := query.VisibilityAll
	switch {
	case f.private && !f.public:
		visibility = query.VisibilityPrivate
	case f.public && !f.private:
		visibility = query.VisibilityPublic
	}
	return query.Filters{
		Owners:          f.owners,
		Visibility:      visibility,
		IncludeForks:    f.forks,
		IncludeArchived: f.archived,
	}
}

// source is a snapshot of the cache: the filtered view plus its metadata.
type source struct {
	repos     []ghapi.Repo
	total     int
	fetchedAt time.Time
	modTime   time.Time
	filters   query.Filters
}

// load reads the cache. On a cold cache it fetches in the foreground, unless
// fetch is false (shell completion must never block on the network).
func (a *app) load(ctx context.Context, f query.Filters, fetch bool) (*source, error) {
	file, err := cache.Load()
	if errors.Is(err, cache.ErrNotExist) {
		if !fetch {
			return nil, err
		}
		file, err = a.fetchCold(ctx)
	}
	if err != nil {
		return nil, err
	}

	mod, _, _ := cache.Stat()
	return &source{
		repos:     query.Filter(file.Repos, f),
		total:     len(file.Repos),
		fetchedAt: file.FetchedAt,
		modTime:   mod,
		filters:   f,
	}, nil
}

// fetchCold populates an empty cache, reporting progress on stderr.
func (a *app) fetchCold(ctx context.Context) (*cache.File, error) {
	fmt.Fprintln(os.Stderr, "repo: no cache yet, fetching your repositories…")

	file, err := refresh.Run(ctx, a.cfg, progressReporter())
	if errors.Is(err, refresh.ErrLocked) {
		// The background refresh beat us to it; wait for it to land.
		return waitForCache(ctx, 30*time.Second)
	}
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "repo: cached %d repositories\n", len(file.Repos))
	return file, nil
}

func progressReporter() func(done, total int) {
	if !isTTY(os.Stderr) {
		return nil
	}
	return func(done, total int) {
		fmt.Fprintf(os.Stderr, "\rrepo: fetching page %d/%d…", done, total)
		if done == total {
			fmt.Fprint(os.Stderr, "\r\033[K")
		}
	}
}

func waitForCache(ctx context.Context, timeout time.Duration) (*cache.File, error) {
	deadline := time.Now().Add(timeout)
	for {
		if file, err := cache.Load(); err == nil {
			return file, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for the background refresh; try `repo sync`")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// interactive reports whether we can run a full-screen picker: the picker reads
// stdin and draws on stderr.
func interactive() bool { return isTTY(os.Stdin) && isTTY(os.Stderr) }

func isTTY(f *os.File) bool {
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
