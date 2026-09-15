package query

import (
	"testing"

	"github.com/isbang/repo/internal/ghapi"
)

func repos() []ghapi.Repo {
	return []ghapi.Repo{
		{FullName: "isbang/repo", Name: "repo", Owner: "isbang"},
		{FullName: "isbang/kube-tools", Name: "kube-tools", Owner: "isbang", Stars: 4},
		{FullName: "acme/kube-tools", Name: "kube-tools", Owner: "acme", Fork: true},
		{FullName: "acme/legacy", Name: "legacy", Owner: "acme", Archived: true, Private: true},
	}
}

func names(results []Result) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Repo.FullName
	}
	return out
}

func TestFilter(t *testing.T) {
	all := repos()
	cases := []struct {
		name  string
		f     Filters
		count int
	}{
		{"everything", Filters{IncludeForks: true, IncludeArchived: true}, 4},
		{"no forks", Filters{IncludeArchived: true}, 3},
		{"no forks or archived", Filters{}, 2},
		{"owner", Filters{Owners: []string{"ACME"}, IncludeForks: true, IncludeArchived: true}, 2},
		{"private only", Filters{Visibility: VisibilityPrivate, IncludeForks: true, IncludeArchived: true}, 1},
		{"public only", Filters{Visibility: VisibilityPublic, IncludeForks: true, IncludeArchived: true}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(Filter(all, tc.f)); got != tc.count {
				t.Errorf("Filter() kept %d, want %d", got, tc.count)
			}
		})
	}
}

func TestRankEmptyQuerySortsByName(t *testing.T) {
	got := names(Rank("  ", repos(), nil))
	want := []string{"acme/kube-tools", "acme/legacy", "isbang/kube-tools", "isbang/repo"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rank(\"\") = %v, want %v", got, want)
		}
	}
}

func TestRankEmptyQueryFloatsFrequentClones(t *testing.T) {
	clones := map[string]int{"isbang/repo": 5, "acme/legacy": 2}
	got := names(Rank("", repos(), clones))
	want := []string{
		"isbang/repo",       // 5 clones
		"acme/legacy",       // 2 clones
		"acme/kube-tools",   // none, then by name
		"isbang/kube-tools", //
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rank(\"\") = %v, want %v", got, want)
		}
	}
}

func TestRankClonesBreakScoreTies(t *testing.T) {
	// Identical fuzzy scores: the more-cloned repository wins.
	all := []ghapi.Repo{
		{FullName: "acme/kube-tools", Name: "kube-tools", Owner: "acme"},
		{FullName: "isbang/kube-tools", Name: "kube-tools", Owner: "isbang"},
	}
	plain := Rank("kube-tools", all, nil)
	if plain[0].Score != plain[1].Score {
		t.Skipf("scores differ (%d vs %d); tie-break not exercised", plain[0].Score, plain[1].Score)
	}
	got := Rank("kube-tools", all, map[string]int{"isbang/kube-tools": 3})
	if got[0].Repo.FullName != "isbang/kube-tools" || got[0].Clones != 3 {
		t.Errorf("first = %q (clones %d), want isbang/kube-tools with 3",
			got[0].Repo.FullName, got[0].Clones)
	}
}

func TestSortByClones(t *testing.T) {
	all := repos()
	SortBy(all, "clones", map[string]int{"acme/legacy": 4})
	if all[0].FullName != "acme/legacy" {
		t.Errorf("first = %q, want acme/legacy", all[0].FullName)
	}
}

func TestRankExactNameWins(t *testing.T) {
	got := names(Rank("repo", repos(), nil))
	if len(got) == 0 || got[0] != "isbang/repo" {
		t.Errorf("Rank(repo) = %v, want isbang/repo first", got)
	}
}

func TestRankDropsNonMatches(t *testing.T) {
	if got := names(Rank("zzz", repos(), nil)); len(got) != 0 {
		t.Errorf("Rank(zzz) = %v, want none", got)
	}
}

func TestUnambiguous(t *testing.T) {
	all := repos()

	t.Run("exact full name", func(t *testing.T) {
		r, ok := Unambiguous("acme/kube-tools", Rank("acme/kube-tools", all, nil))
		if !ok || r.FullName != "acme/kube-tools" {
			t.Errorf("got %q, %v", r.FullName, ok)
		}
	})

	t.Run("unique name", func(t *testing.T) {
		r, ok := Unambiguous("repo", Rank("repo", all, nil))
		if !ok || r.FullName != "isbang/repo" {
			t.Errorf("got %q, %v", r.FullName, ok)
		}
	})

	t.Run("duplicate name is ambiguous", func(t *testing.T) {
		if r, ok := Unambiguous("kube-tools", Rank("kube-tools", all, nil)); ok {
			t.Errorf("want ambiguous, got %q", r.FullName)
		}
	})

	t.Run("single fuzzy match", func(t *testing.T) {
		r, ok := Unambiguous("lgc", Rank("lgc", all, nil))
		if !ok || r.FullName != "acme/legacy" {
			t.Errorf("got %q, %v", r.FullName, ok)
		}
	})

	t.Run("no results", func(t *testing.T) {
		if _, ok := Unambiguous("zzz", Rank("zzz", all, nil)); ok {
			t.Error("want not ok")
		}
	})
}

func TestOwners(t *testing.T) {
	got := Owners(repos())
	want := []string{"acme", "isbang"}
	if len(got) != len(want) {
		t.Fatalf("Owners() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Owners() = %v, want %v", got, want)
		}
	}
}
