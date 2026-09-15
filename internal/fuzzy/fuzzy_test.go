package fuzzy

import "testing"

func TestMatchAcceptsSubsequences(t *testing.T) {
	cases := []struct {
		pattern, text string
		want          bool
	}{
		{"repo", "isbang/repo", true},
		{"kt", "isbang/kube-tools", true},
		{"isrepo", "isbang/repo", true},
		{"", "isbang/repo", true},
		{"xyz", "isbang/repo", false},
		{"oper", "isbang/repo", false}, // out of order
		{"REPO", "isbang/repo", false}, // smart case: uppercase is significant
		{"Repo", "isbang/Repo", true},
	}
	for _, tc := range cases {
		if _, ok := Match(tc.pattern, tc.text); ok != tc.want {
			t.Errorf("Match(%q, %q) ok = %v, want %v", tc.pattern, tc.text, ok, tc.want)
		}
	}
}

func TestMatchPrefersBoundariesAndRuns(t *testing.T) {
	cases := []struct {
		name          string
		pattern       string
		better, worse string
	}{
		{"name beats owner", "kube", "acme/kube-tools", "kubernetes/tools"},
		{"consecutive beats scattered", "ktl", "acme/ktl", "acme/kotlin-lang"},
		{"word boundary beats mid-word", "gt", "acme/git-tools", "acme/gothic"},
		{"start of name beats later", "tools", "acme/tools-cli", "acme/cli-tools-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, ok := Match(tc.pattern, tc.better)
			if !ok {
				t.Fatalf("Match(%q, %q) did not match", tc.pattern, tc.better)
			}
			w, ok := Match(tc.pattern, tc.worse)
			if !ok {
				t.Fatalf("Match(%q, %q) did not match", tc.pattern, tc.worse)
			}
			if b <= w {
				t.Errorf("Match(%q): %q scored %d, want more than %q at %d",
					tc.pattern, tc.better, b, tc.worse, w)
			}
		})
	}
}

func TestMatchANDsTerms(t *testing.T) {
	if _, ok := Match("isbang repo", "isbang/repo"); !ok {
		t.Error("both terms match, want ok")
	}
	if _, ok := Match("isbang nope", "isbang/repo"); ok {
		t.Error("second term does not match, want not ok")
	}
}

func TestMatchPositions(t *testing.T) {
	score, pos, ok := MatchPositions("repo", "isbang/repo")
	if !ok {
		t.Fatal("want a match")
	}
	if score <= 0 {
		t.Errorf("score = %d, want > 0", score)
	}
	want := []int{7, 8, 9, 10}
	if len(pos) != len(want) {
		t.Fatalf("positions = %v, want %v", pos, want)
	}
	for i := range want {
		if pos[i] != want[i] {
			t.Fatalf("positions = %v, want %v", pos, want)
		}
	}
}

func TestMatchPositionsAreSortedAndUniqueAcrossTerms(t *testing.T) {
	_, pos, ok := MatchPositions("repo is", "isbang/repo")
	if !ok {
		t.Fatal("want a match")
	}
	for i := 1; i < len(pos); i++ {
		if pos[i] <= pos[i-1] {
			t.Fatalf("positions not sorted/unique: %v", pos)
		}
	}
}

func BenchmarkMatch(b *testing.B) {
	for b.Loop() {
		Match("kube", "kubernetes-sigs/kustomize-controller")
	}
}
