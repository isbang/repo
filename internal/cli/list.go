package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/history"
	"github.com/isbang/repo/internal/humanize"
	"github.com/isbang/repo/internal/query"
)

type outputFlags struct {
	json  bool
	long  bool
	limit int
}

func (a *app) newListCmd() *cobra.Command {
	var (
		out  outputFlags
		sort string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the cached repositories",
		Long: `List the repositories in the cache, one "owner/name" per line — ready to pipe
into fzf, xargs or anything else.`,
		Args: cobra.NoArgs,
		Example: `  repo list
  repo list --owner isbang --private
  repo list --sort stars --long | head
  repo list --json | jq -r '.[].ssh_url'`,
	}
	f := a.registerFilterFlags(cmd)
	registerOutputFlags(cmd, &out, 0)
	cmd.Flags().StringVar(&sort, "sort", "name", "sort order: name, clones, stars or pushed")
	_ = cmd.RegisterFlagCompletionFunc("sort", fixedCompletions("name", "clones", "stars", "pushed"))

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		src, err := a.load(cmd.Context(), f.filters(), true)
		if err != nil {
			return err
		}
		repos := src.repos
		query.SortBy(repos, sort, history.Counts())
		if out.limit > 0 && len(repos) > out.limit {
			repos = repos[:out.limit]
		}
		return writeRepos(repos, out)
	}
	return cmd
}

func registerOutputFlags(cmd *cobra.Command, out *outputFlags, defaultLimit int) {
	flags := cmd.Flags()
	flags.BoolVar(&out.json, "json", false, "output JSON")
	flags.BoolVarP(&out.long, "long", "l", false, "output a table with stars, language and description")
	flags.IntVarP(&out.limit, "limit", "n", defaultLimit, "maximum number of results (0 for all)")
}

func writeRepos(repos []ghapi.Repo, out outputFlags) error {
	switch {
	case out.json:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if repos == nil {
			repos = []ghapi.Repo{}
		}
		return enc.Encode(repos)

	case out.long:
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, r := range repos {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				r.FullName,
				dash(r.Language),
				tags(r),
				truncateRunes(r.Description, 60),
			)
		}
		return w.Flush()

	default:
		var b strings.Builder
		for _, r := range repos {
			b.WriteString(r.FullName)
			b.WriteByte('\n')
		}
		_, err := os.Stdout.WriteString(b.String())
		return err
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func tags(r ghapi.Repo) string {
	var parts []string
	if r.Private {
		parts = append(parts, "private")
	}
	if r.Fork {
		parts = append(parts, "fork")
	}
	if r.Archived {
		parts = append(parts, "archived")
	}
	if !r.PushedAt.IsZero() {
		parts = append(parts, humanize.Ago(r.PushedAt))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

func truncateRunes(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
