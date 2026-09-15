package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/isbang/repo/internal/cache"
	"github.com/isbang/repo/internal/history"
	"github.com/isbang/repo/internal/query"
)

// completionLimit caps how many candidates are handed to the shell.
const completionLimit = 200

// completeRepos completes a repository query from the cache.
//
// Candidates are the bare repository name when it is unambiguous (that is how
// most queries are typed) and "owner/name" otherwise, or whenever the word being
// completed already contains a slash. It never hits the network: shell
// completion has to stay instant even with a cold cache.
func (a *app) completeRepos(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	file, err := cache.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	repos := query.Filter(file.Repos, query.Filters{
		IncludeForks:    a.cfg.Forks(),
		IncludeArchived: a.cfg.Archived(),
	})

	names := make(map[string]int, len(repos))
	for _, r := range repos {
		names[strings.ToLower(r.Name)]++
	}

	qualified := strings.Contains(toComplete, "/")
	results := query.Rank(toComplete, repos, history.Counts())
	out := make([]string, 0, min(len(results), completionLimit))
	for i, res := range results {
		if i == completionLimit {
			break
		}
		candidate := res.Repo.FullName
		if !qualified && names[strings.ToLower(res.Repo.Name)] == 1 {
			candidate = res.Repo.Name
		}
		out = append(out, candidate+"\t"+completionDescription(res))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completionDescription(res query.Result) string {
	var parts []string
	if res.Repo.FullName != res.Repo.Name {
		parts = append(parts, res.Repo.FullName)
	}
	if res.Repo.Private {
		parts = append(parts, "private")
	}
	if res.Repo.Archived {
		parts = append(parts, "archived")
	}
	if desc := res.Repo.Description; desc != "" {
		parts = append(parts, truncateRunes(desc, 60))
	}
	return strings.Join(parts, " · ")
}

// completeOwners completes --owner values from the cache.
func (a *app) completeOwners(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	file, err := cache.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	lower := strings.ToLower(toComplete)
	owners := query.Owners(file.Repos)

	counts := map[string]int{}
	for _, r := range file.Repos {
		counts[r.Owner]++
	}

	out := make([]string, 0, len(owners))
	for _, owner := range owners {
		if lower != "" && !strings.HasPrefix(strings.ToLower(owner), lower) {
			continue
		}
		out = append(out, fmt.Sprintf("%s\t%s", owner, plural(counts[owner], "repository", "repositories")))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// fixedCompletions completes a flag from a fixed set of values.
func fixedCompletions(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}
