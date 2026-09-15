package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/history"
	"github.com/isbang/repo/internal/query"
)

func (a *app) newSearchCmd() *cobra.Command {
	var (
		out        outputFlags
		showScores bool
	)
	cmd := &cobra.Command{
		Use:     "search <query...>",
		Aliases: []string{"find"},
		Short:   "Fuzzy-search the cached repositories",
		Long: `Rank the cached repositories against a fuzzy query and print the matches, best
first. Space-separated terms are ANDed, and matching is case-insensitive unless
the query contains an uppercase letter.`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: a.completeRepos,
		Example: `  repo search kube
  repo search isbang cli --limit 5
  repo search repo --score`,
	}
	f := a.registerFilterFlags(cmd)
	registerOutputFlags(cmd, &out, 30)
	cmd.Flags().BoolVar(&showScores, "score", false, "prefix each line with its match score")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		src, err := a.load(cmd.Context(), f.filters(), true)
		if err != nil {
			return err
		}
		results := query.Rank(strings.Join(args, " "), src.repos, history.Counts())
		if out.limit > 0 && len(results) > out.limit {
			results = results[:out.limit]
		}
		if len(results) == 0 {
			return fmt.Errorf("no repository matches %q", strings.Join(args, " "))
		}

		if showScores && !out.json {
			for _, r := range results {
				fmt.Fprintf(os.Stdout, "%7d  %s\n", r.Score, r.Repo.FullName)
			}
			return nil
		}

		repos := make([]ghapi.Repo, len(results))
		for i, r := range results {
			repos[i] = r.Repo
		}
		return writeRepos(repos, out)
	}
	return cmd
}
