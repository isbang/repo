// Package query filters and ranks cached repositories.
package query

import (
	"sort"
	"strings"

	"github.com/isbang/repo/internal/fuzzy"
	"github.com/isbang/repo/internal/ghapi"
)

// Extra score added on top of the fuzzy score for unambiguous intent.
const (
	bonusExactFullName = 100_000
	bonusExactName     = 50_000
	bonusNamePrefix    = 200
)

// Visibility filters on public/private repositories.
const (
	VisibilityAll     = ""
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

// Filters narrows a repository list before ranking.
type Filters struct {
	Owners          []string
	Visibility      string
	IncludeForks    bool
	IncludeArchived bool
}

// Filter applies f, preserving the input order.
func Filter(repos []ghapi.Repo, f Filters) []ghapi.Repo {
	owners := make(map[string]struct{}, len(f.Owners))
	for _, o := range f.Owners {
		owners[strings.ToLower(o)] = struct{}{}
	}

	out := make([]ghapi.Repo, 0, len(repos))
	for _, r := range repos {
		if !f.IncludeForks && r.Fork {
			continue
		}
		if !f.IncludeArchived && r.Archived {
			continue
		}
		switch f.Visibility {
		case VisibilityPublic:
			if r.Private {
				continue
			}
		case VisibilityPrivate:
			if !r.Private {
				continue
			}
		}
		if len(owners) > 0 {
			if _, ok := owners[strings.ToLower(r.Owner)]; !ok {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// Result is one ranked repository.
type Result struct {
	Repo  ghapi.Repo
	Score int
	// Clones is how often this repository was cloned recently (see the history
	// package); it lifts familiar repositories towards the top.
	Clones int
}

// Rank returns the repositories matching q, best first.
//
// clones maps "owner/name" to a recent clone count and may be nil. With no
// query the order is most-cloned first, then by name — so the picker opens on
// the repositories you actually work with. With a query, fuzzy relevance leads
// and the clone count breaks ties.
func Rank(q string, repos []ghapi.Repo, clones map[string]int) []Result {
	q = strings.TrimSpace(q)

	if q == "" {
		out := make([]Result, len(repos))
		for i, r := range repos {
			out[i] = Result{Repo: r, Clones: clones[r.FullName]}
		}
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if a.Clones != b.Clones {
				return a.Clones > b.Clones
			}
			return strings.ToLower(a.Repo.FullName) < strings.ToLower(b.Repo.FullName)
		})
		return out
	}

	lower := strings.ToLower(q)
	out := make([]Result, 0, len(repos))
	for _, r := range repos {
		score, ok := fuzzy.Match(q, r.FullName)
		if !ok {
			continue
		}
		switch {
		case strings.EqualFold(r.FullName, q):
			score += bonusExactFullName
		case strings.EqualFold(r.Name, q):
			score += bonusExactName
		case strings.HasPrefix(strings.ToLower(r.Name), lower):
			score += bonusNamePrefix
		}
		out = append(out, Result{Repo: r, Score: score, Clones: clones[r.FullName]})
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Clones != b.Clones {
			return a.Clones > b.Clones
		}
		// Shorter names are the likelier target.
		if len(a.Repo.FullName) != len(b.Repo.FullName) {
			return len(a.Repo.FullName) < len(b.Repo.FullName)
		}
		return strings.ToLower(a.Repo.FullName) < strings.ToLower(b.Repo.FullName)
	})
	return out
}

// Positions reports the matched rune indices of q in the repository's full name.
func Positions(q string, fullName string) []int {
	if strings.TrimSpace(q) == "" {
		return nil
	}
	_, pos, _ := fuzzy.MatchPositions(q, fullName)
	return pos
}

// Unambiguous reports the single repository q clearly refers to: an exact
// "owner/name", an exact repository name matched by exactly one repository, or
// a query that only one repository matches at all.
func Unambiguous(q string, results []Result) (ghapi.Repo, bool) {
	q = strings.TrimSpace(q)
	if q == "" || len(results) == 0 {
		return ghapi.Repo{}, false
	}
	if len(results) == 1 {
		return results[0].Repo, true
	}
	for _, r := range results {
		if strings.EqualFold(r.Repo.FullName, q) {
			return r.Repo, true
		}
	}
	var (
		match ghapi.Repo
		count int
	)
	for _, r := range results {
		if strings.EqualFold(r.Repo.Name, q) {
			match, count = r.Repo, count+1
		}
	}
	if count == 1 {
		return match, true
	}
	return ghapi.Repo{}, false
}

// SortBy reorders repos in place by key: "name", "clones", "stars" or "pushed".
// clones may be nil unless key is "clones".
func SortBy(repos []ghapi.Repo, key string, clones map[string]int) {
	switch key {
	case "clones":
		sort.SliceStable(repos, func(i, j int) bool {
			a, b := clones[repos[i].FullName], clones[repos[j].FullName]
			if a != b {
				return a > b
			}
			return strings.ToLower(repos[i].FullName) < strings.ToLower(repos[j].FullName)
		})
	case "stars":
		sort.SliceStable(repos, func(i, j int) bool { return repos[i].Stars > repos[j].Stars })
	case "pushed":
		sort.SliceStable(repos, func(i, j int) bool { return repos[i].PushedAt.After(repos[j].PushedAt) })
	default:
		sort.SliceStable(repos, func(i, j int) bool {
			return strings.ToLower(repos[i].FullName) < strings.ToLower(repos[j].FullName)
		})
	}
}

// Owners returns the distinct owners in repos, sorted.
func Owners(repos []ghapi.Repo) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, r := range repos {
		if _, ok := seen[r.Owner]; ok {
			continue
		}
		seen[r.Owner] = struct{}{}
		out = append(out, r.Owner)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}
