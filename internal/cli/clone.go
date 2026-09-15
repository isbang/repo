package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/cache"
	"github.com/isbang/repo/internal/cloner"
	"github.com/isbang/repo/internal/config"
	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/gitx"
	"github.com/isbang/repo/internal/history"
	"github.com/isbang/repo/internal/query"
	"github.com/isbang/repo/internal/refresh"
	"github.com/isbang/repo/internal/tui"
)

// reportInterval is how often finished clones are reported after the picker
// closes.
const reportInterval = 150 * time.Millisecond

type cloneFlags struct {
	dir       string
	name      string
	protocol  string
	jobs      int
	pick      bool
	first     bool
	printPath bool
	dryRun    bool
	depth     int
	branch    string
	filter    *filterFlags
}

func (a *app) newCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone [query...] [-- git-args...]",
		Short: "Clone repositories chosen by fuzzy search",
		Long: `Clone repositories.

With no query the interactive picker opens: enter queues the highlighted
repository, a background worker clones it, and the picker stays open so you can
queue as many repositories as you like. Quit with esc once you are done; repo
waits for the remaining clones and reports the result.

With a query, an unambiguous match (an exact "owner/name", a unique repository
name, or the only fuzzy match) is cloned right away in the foreground; anything
else opens the picker pre-filled with the query, or fails with the candidates
listed when there is no terminal.

Arguments after -- are passed through to git clone.`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: a.completeRepos,
		Example: `  repo clone                     # queue as many as you like, interactively
  repo clone kube-tools          # clone the unique match
  repo clone isbang/repo -C ~/src
  repo clone repo -- --depth 1 --recurse-submodules`,
	}
	f := a.registerCloneFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return a.runClone(cmd, args, f)
	}
	return cmd
}

func (a *app) registerCloneFlags(cmd *cobra.Command) *cloneFlags {
	f := &cloneFlags{}
	flags := cmd.Flags()
	flags.StringVarP(&f.dir, "dir", "C", "", "parent directory to clone into (default: current directory)")
	flags.StringVarP(&f.name, "name", "o", "", "directory name for the clone (default: the repository name)")
	flags.StringVar(&f.protocol, "protocol", a.cfg.Protocol, "clone protocol: https or ssh")
	flags.IntVarP(&f.jobs, "jobs", "j", a.cfg.CloneConcurrency, "how many queued clones run at once")
	flags.BoolVarP(&f.pick, "select", "s", false, "always open the picker, even with a query")
	flags.BoolVar(&f.first, "first", false, "take the best match without asking")
	flags.BoolVar(&f.printPath, "print-path", false, "print each cloned path on stdout")
	flags.BoolVar(&f.dryRun, "dry-run", false, "print the git command instead of running it")
	flags.IntVar(&f.depth, "depth", 0, "create a shallow clone with this depth")
	flags.StringVarP(&f.branch, "branch", "b", "", "checkout this branch instead of the default one")
	_ = cmd.RegisterFlagCompletionFunc("protocol", fixedCompletions(config.ProtocolHTTPS, config.ProtocolSSH))
	_ = cmd.MarkFlagDirname("dir")

	f.filter = a.registerFilterFlags(cmd)
	return f
}

func (a *app) runClone(cmd *cobra.Command, args []string, f *cloneFlags) error {
	if err := gitx.Available(); err != nil {
		return err
	}
	if f.protocol != config.ProtocolHTTPS && f.protocol != config.ProtocolSSH {
		return fmt.Errorf("invalid --protocol %q: want %q or %q", f.protocol, config.ProtocolHTTPS, config.ProtocolSSH)
	}

	// Everything after -- belongs to git.
	queryArgs, gitArgs := args, []string(nil)
	if n := cmd.ArgsLenAtDash(); n >= 0 {
		queryArgs, gitArgs = args[:n], args[n:]
	}

	ctx := cmd.Context()
	src, err := a.load(ctx, f.filter.filters(), true)
	if err != nil {
		return err
	}
	if len(src.repos) == 0 {
		return fmt.Errorf("no repositories in the cache (%d before filtering); try `repo sync`", src.total)
	}

	clones := history.Counts()
	q := strings.Join(queryArgs, " ")

	// A query that names one repository is cloned straight away: that is what a
	// script or a one-off `repo clone foo` wants.
	if q != "" && !f.pick {
		results := query.Rank(q, src.repos, clones)
		if len(results) == 0 {
			return fmt.Errorf("no repository matches %q (%d cached)", q, len(src.repos))
		}
		switch repo, ok := query.Unambiguous(q, results); {
		case ok:
			return a.cloneNow(ctx, repo, f, gitArgs)
		case f.first:
			return a.cloneNow(ctx, results[0].Repo, f, gitArgs)
		case !interactive():
			return ambiguous(q, results)
		}
	}

	return a.runQueue(ctx, src, q, clones, f, gitArgs)
}

// cloneNow clones one repository in the foreground, with git writing straight to
// the terminal.
func (a *app) cloneNow(ctx context.Context, repo ghapi.Repo, f *cloneFlags, gitArgs []string) error {
	dest, err := a.destination(repo, f)
	if err != nil {
		return err
	}
	args := append(a.gitArgs(f), gitArgs...)
	url := repo.CloneURLFor(f.protocol)

	if f.dryRun {
		fmt.Println(cloner.Job{Repo: repo, Dest: dest, URL: url, Args: args}.Command())
		return nil
	}
	if err := gitx.CheckDest(dest); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Cloning %s into %s\n", repo.FullName, dest)
	if err := gitx.Clone(ctx, gitx.Options{
		URL:  url,
		Dest: dest,
		Args: args,
		// git writes progress to stderr; keep stdout clean for --print-path.
		Stdout: os.Stderr,
		Stderr: os.Stderr,
	}); err != nil {
		return err
	}

	recordClone(repo.FullName)
	if f.printPath {
		fmt.Println(dest)
	}
	return nil
}

// runQueue opens the picker and clones whatever the user queues, in the
// background, until they quit.
func (a *app) runQueue(ctx context.Context, src *source, q string, clones map[string]int, f *cloneFlags, gitArgs []string) error {
	if !interactive() {
		return errors.New("no terminal available for the interactive picker; pass a query instead")
	}

	queue := cloner.New(ctx, cloner.Options{Concurrency: f.jobs, DryRun: f.dryRun})
	args := append(a.gitArgs(f), gitArgs...)

	enqueue := func(repo ghapi.Repo) (string, bool) {
		dest, err := a.destination(repo, f)
		if err != nil {
			return err.Error(), false
		}
		if _, err := queue.Enqueue(repo, dest, repo.CloneURLFor(f.protocol), args); err != nil {
			return fmt.Sprintf("%s: %v", repo.FullName, err), false
		}
		if f.dryRun {
			return "dry run: " + repo.FullName, true
		}
		return fmt.Sprintf("queued %s → %s", repo.FullName, dest), true
	}

	runErr := tui.Run(tui.Options{
		Repos:      src.repos,
		FetchedAt:  src.fetchedAt,
		Query:      q,
		Clones:     clones,
		Enqueue:    enqueue,
		Jobs:       queue.Snapshot,
		Reload:     a.reloader(src),
		Refreshing: refresh.InProgress,
		Refresh:    func() { _ = refresh.Spawn() },
	})

	queue.Close()
	if runErr != nil {
		queue.Wait()
		return runErr
	}
	return a.reportQueue(queue, f)
}

// reloader watches the cache file for the picker, so a background refresh shows
// up without restarting.
func (a *app) reloader(src *source) func() ([]ghapi.Repo, time.Time, bool) {
	lastMod := src.modTime
	return func() ([]ghapi.Repo, time.Time, bool) {
		mod, _, err := cache.Stat()
		if err != nil || !mod.After(lastMod) {
			return nil, time.Time{}, false
		}
		file, err := cache.Load()
		if err != nil {
			return nil, time.Time{}, false
		}
		lastMod = mod
		return query.Filter(file.Repos, src.filters), file.FetchedAt, true
	}
}

// reportQueue waits for the queue to drain, reporting each job as it finishes.
func (a *app) reportQueue(queue *cloner.Queue, f *cloneFlags) error {
	if pending := queue.Counts().Pending(); pending > 0 {
		fmt.Fprintf(os.Stderr, "waiting for %s…\n", plural(pending, "clone", "clones"))
	}

	reported := map[int]bool{}
	for {
		jobs := queue.Snapshot()
		pending := 0
		for _, job := range jobs {
			if !job.State.Terminal() {
				pending++
				continue
			}
			if reported[job.ID] {
				continue
			}
			reported[job.ID] = true
			a.reportJob(job, f)
		}
		if pending == 0 {
			break
		}
		time.Sleep(reportInterval)
	}
	queue.Wait()

	counts := queue.Counts()
	if counts.Total() == 0 {
		// Nothing was queued: the user just looked around.
		return tui.ErrAborted
	}
	fmt.Fprintf(os.Stderr, "%s\n", queueSummary(counts))
	if counts.Failed > 0 {
		return fmt.Errorf("%s failed", plural(counts.Failed, "clone", "clones"))
	}
	return nil
}

func (a *app) reportJob(job cloner.Job, f *cloneFlags) {
	switch job.State {
	case cloner.Done:
		fmt.Fprintf(os.Stderr, "✓ %s → %s (%s)\n", job.Repo.FullName, job.Dest,
			job.Elapsed().Round(100*time.Millisecond))
		recordClone(job.Repo.FullName)
		if f.printPath {
			fmt.Println(job.Dest)
		}
	case cloner.Failed:
		fmt.Fprintf(os.Stderr, "✗ %s: %v\n", job.Repo.FullName, job.Err)
		if line := lastLine(job.Output); line != "" {
			fmt.Fprintf(os.Stderr, "  %s\n", line)
		}
	case cloner.Skipped:
		if f.dryRun {
			fmt.Println(job.Command())
			return
		}
		fmt.Fprintf(os.Stderr, "⊘ %s: %v\n", job.Repo.FullName, job.Err)
	}
}

func queueSummary(c cloner.Counts) string {
	parts := []string{fmt.Sprintf("%s queued", plural(c.Total(), "clone", "clones"))}
	for _, p := range []struct {
		n     int
		label string
	}{
		{c.Done, "cloned"},
		{c.Failed, "failed"},
		{c.Skipped, "skipped"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.label))
		}
	}
	return strings.Join(parts, ", ")
}

// recordClone remembers a clone for ranking. A failure here must never fail the
// command: it only affects ordering next time.
func recordClone(fullName string) {
	if err := history.Record(fullName); err != nil && os.Getenv("REPO_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "repo: clone history: %v\n", err)
	}
}

func ambiguous(q string, results []query.Result) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d repositories; be more specific or pass --first:", q, len(results))
	for i, r := range results {
		if i == 5 {
			fmt.Fprintf(&b, "\n  … and %d more", len(results)-i)
			break
		}
		fmt.Fprintf(&b, "\n  %s", r.Repo.FullName)
	}
	return fmt.Errorf("%s", b.String())
}

// destination is the absolute path a clone lands in.
func (a *app) destination(repo ghapi.Repo, f *cloneFlags) (string, error) {
	parent := f.dir
	if parent == "" {
		parent = a.cfg.CloneDir
	}
	if parent == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		parent = wd
	}
	name := f.name
	if name == "" {
		name = repo.Name
	}
	return filepath.Abs(filepath.Join(parent, name))
}

func (a *app) gitArgs(f *cloneFlags) []string {
	args := append([]string(nil), a.cfg.GitArgs...)
	if f.depth > 0 {
		args = append(args, "--depth", strconv.Itoa(f.depth))
	}
	if f.branch != "" {
		args = append(args, "--branch", f.branch)
	}
	return args
}

func lastLine(s string) string {
	lines := lastLines(s, 1)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}
