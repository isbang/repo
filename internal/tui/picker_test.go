package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/isbang/repo/internal/cloner"
	"github.com/isbang/repo/internal/ghapi"
)

func testRepos() []ghapi.Repo {
	return []ghapi.Repo{
		{FullName: "isbang/repo", Name: "repo", Owner: "isbang", Description: "this tool"},
		{FullName: "isbang/kube-tools", Name: "kube-tools", Owner: "isbang", Language: "Go"},
		{FullName: "acme/legacy", Name: "legacy", Owner: "acme", Private: true, Archived: true},
	}
}

// recorder stands in for the clone queue.
type recorder struct {
	queued []string
	jobs   []cloner.Job
	reject bool
}

func (r *recorder) enqueue(repo ghapi.Repo) (string, bool) {
	if r.reject {
		return repo.FullName + ": already queued", false
	}
	r.queued = append(r.queued, repo.FullName)
	r.jobs = append(r.jobs, cloner.Job{
		ID:    len(r.jobs) + 1,
		Repo:  repo,
		Dest:  "/tmp/" + repo.Name,
		State: cloner.Queued,
	})
	return "queued " + repo.FullName, true
}

func (r *recorder) snapshot() []cloner.Job { return r.jobs }

func newTestModel(t *testing.T, opts Options) model {
	t.Helper()
	if opts.Repos == nil {
		opts.Repos = testRepos()
	}
	if opts.FetchedAt.IsZero() {
		opts.FetchedAt = time.Now()
	}
	m := newModel(opts)
	return m.send(t, tea.WindowSizeMsg{Width: 100, Height: 20})
}

// send applies a message and returns the resulting model.
func (m model) send(t *testing.T, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	out, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return out
}

func typeString(t *testing.T, m model, s string) model {
	t.Helper()
	return m.send(t, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestPickerStartsWithEverything(t *testing.T) {
	m := newTestModel(t, Options{})
	if len(m.results) != 3 {
		t.Fatalf("results = %d, want 3", len(m.results))
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

func TestPickerOrdersByCloneCount(t *testing.T) {
	m := newTestModel(t, Options{Clones: map[string]int{"acme/legacy": 3}})
	if got := m.results[0].Repo.FullName; got != "acme/legacy" {
		t.Errorf("first row = %q, want acme/legacy", got)
	}
}

func TestPickerFiltersAsYouType(t *testing.T) {
	m := typeString(t, newTestModel(t, Options{}), "kube")
	if len(m.results) != 1 {
		t.Fatalf("results = %d, want 1", len(m.results))
	}
	if got := m.results[0].Repo.FullName; got != "isbang/kube-tools" {
		t.Errorf("match = %q, want isbang/kube-tools", got)
	}
}

func TestPickerEnterQueuesAndStaysOpen(t *testing.T) {
	q := &recorder{}
	m := typeString(t, newTestModel(t, Options{Enqueue: q.enqueue, Jobs: q.snapshot}), "kube")
	m = m.send(t, tea.KeyMsg{Type: tea.KeyEnter})

	if len(q.queued) != 1 || q.queued[0] != "isbang/kube-tools" {
		t.Fatalf("queued = %v, want [isbang/kube-tools]", q.queued)
	}
	if !strings.Contains(m.status, "queued isbang/kube-tools") {
		t.Errorf("status = %q", m.status)
	}
	// The picker keeps its list so more repositories can be queued.
	if len(m.results) != 1 {
		t.Errorf("results = %d, want the list to stay", len(m.results))
	}
	if m.jobState["isbang/kube-tools"] != cloner.Queued {
		t.Errorf("row marker state = %v, want queued", m.jobState["isbang/kube-tools"])
	}
}

func TestPickerQueuesSeveralRepositories(t *testing.T) {
	q := &recorder{}
	m := newTestModel(t, Options{Enqueue: q.enqueue, Jobs: q.snapshot})
	m = m.send(t, tea.KeyMsg{Type: tea.KeyEnter})
	m = m.send(t, tea.KeyMsg{Type: tea.KeyEnter})
	m = m.send(t, tea.KeyMsg{Type: tea.KeyEnter})

	want := []string{"acme/legacy", "isbang/kube-tools", "isbang/repo"}
	if len(q.queued) != len(want) {
		t.Fatalf("queued = %v, want %v", q.queued, want)
	}
	for i := range want {
		if q.queued[i] != want[i] {
			t.Fatalf("queued = %v, want %v (enter should advance the cursor)", q.queued, want)
		}
	}
}

func TestPickerRejectedEnqueueKeepsCursor(t *testing.T) {
	q := &recorder{reject: true}
	m := newTestModel(t, Options{Enqueue: q.enqueue, Jobs: q.snapshot})
	before := m.cursor
	m = m.send(t, tea.KeyMsg{Type: tea.KeyEnter})
	if m.cursor != before {
		t.Errorf("cursor = %d, want it to stay at %d", m.cursor, before)
	}
	if !strings.Contains(m.status, "already queued") {
		t.Errorf("status = %q, want the rejection reason", m.status)
	}
}

func TestPickerEnterWithNoMatchQueuesNothing(t *testing.T) {
	q := &recorder{}
	m := typeString(t, newTestModel(t, Options{Enqueue: q.enqueue, Jobs: q.snapshot}), "zzzz")
	if len(m.results) != 0 {
		t.Fatalf("results = %d, want 0", len(m.results))
	}
	m = m.send(t, tea.KeyMsg{Type: tea.KeyEnter})
	if len(q.queued) != 0 {
		t.Errorf("queued = %v, want nothing", q.queued)
	}
}

func TestPickerCursorStaysInRange(t *testing.T) {
	m := newTestModel(t, Options{})
	for range 10 {
		m = m.send(t, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != len(m.results)-1 {
		t.Errorf("cursor = %d, want %d", m.cursor, len(m.results)-1)
	}
	for range 10 {
		m = m.send(t, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}
}

func TestPickerPrefillsQuery(t *testing.T) {
	m := newTestModel(t, Options{Query: "legacy"})
	if len(m.results) != 1 || m.results[0].Repo.FullName != "acme/legacy" {
		t.Fatalf("results = %v", m.results)
	}
}

func TestPickerReloadKeepsSelection(t *testing.T) {
	repos := testRepos()
	reloaded := false
	m := newTestModel(t, Options{
		Repos: repos,
		Reload: func() ([]ghapi.Repo, time.Time, bool) {
			if reloaded {
				return nil, time.Time{}, false
			}
			reloaded = true
			// A refresh that adds a repository ahead of the selected one.
			grown := append([]ghapi.Repo{{FullName: "aaa/new", Name: "new", Owner: "aaa"}}, repos...)
			return grown, time.Now(), true
		},
	})
	m = m.send(t, tea.KeyMsg{Type: tea.KeyDown})
	want := m.results[m.cursor].Repo.FullName

	m = m.send(t, tickMsg(time.Now()))
	if len(m.repos) != 4 {
		t.Fatalf("repos = %d, want 4 after reload", len(m.repos))
	}
	if got := m.results[m.cursor].Repo.FullName; got != want {
		t.Errorf("selection moved to %q, want %q", got, want)
	}
}

func TestPickerViewShowsRowsAndHelp(t *testing.T) {
	view := newTestModel(t, Options{Clones: map[string]int{"isbang/repo": 4}}).View()
	for _, want := range []string{"isbang/repo", "isbang/kube-tools", "×4", "Go", "private", "archived", "enter queue clone"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestPickerViewShowsQueuePanel(t *testing.T) {
	jobs := []cloner.Job{
		{ID: 1, Repo: ghapi.Repo{FullName: "isbang/repo", Name: "repo"}, Dest: "/tmp/repo",
			State: cloner.Done, StartedAt: time.Now().Add(-2 * time.Second), FinishedAt: time.Now()},
		{ID: 2, Repo: ghapi.Repo{FullName: "acme/legacy", Name: "legacy"}, Dest: "/tmp/legacy",
			State: cloner.Failed, Err: errors.New("repository not found")},
		{ID: 3, Repo: ghapi.Repo{FullName: "isbang/kube-tools", Name: "kube-tools"}, Dest: "/tmp/kube-tools",
			State: cloner.Running, StartedAt: time.Now().Add(-time.Second)},
	}
	m := newTestModel(t, Options{Jobs: func() []cloner.Job { return jobs }})
	m = m.send(t, tickMsg(time.Now()))

	view := m.View()
	for _, want := range []string{"queue 3", "1 cloning", "1 done", "1 failed", "repository not found", "/tmp/repo"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// Running jobs come first in the panel.
	if got := m.panelJobs(); got[0].State != cloner.Running {
		t.Errorf("first panel job state = %v, want running", got[0].State)
	}
}

func TestPickerPanelShrinksList(t *testing.T) {
	m := newTestModel(t, Options{})
	empty := m.listHeight()

	jobs := []cloner.Job{{ID: 1, Repo: ghapi.Repo{FullName: "isbang/repo"}, State: cloner.Queued}}
	m.opts.Jobs = func() []cloner.Job { return jobs }
	m = m.send(t, tickMsg(time.Now()))

	if m.listHeight() >= empty {
		t.Errorf("listHeight = %d with a queue panel, want less than %d", m.listHeight(), empty)
	}
}

func TestHighlightBatchesRuns(t *testing.T) {
	got := highlight("isbang/repo", []int{7, 8, 9, 10}, nameStyle, hitStyle, 40)
	if !strings.Contains(got, "isbang/") || !strings.Contains(got, "repo") {
		t.Errorf("highlight() = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{"0123456789", "01234567…"},
	}
	for _, tc := range cases {
		if got := truncate(tc.in, 9); got != tc.want {
			t.Errorf("truncate(%q, 9) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := truncate("abc", 0); got != "" {
		t.Errorf("truncate(abc, 0) = %q, want empty", got)
	}
}
