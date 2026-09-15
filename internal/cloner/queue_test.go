package cloner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isbang/repo/internal/ghapi"
)

func repo(name string) ghapi.Repo {
	return ghapi.Repo{FullName: "isbang/" + name, Name: name, Owner: "isbang"}
}

// waitFor polls until cond holds, failing the test on timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEnqueueClonesLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin := initOrigin(t)
	dest := filepath.Join(t.TempDir(), "clone")

	q := New(context.Background(), Options{Concurrency: 1})
	if _, err := q.Enqueue(repo("origin"), dest, origin, nil); err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	q.Close()
	q.Wait()

	jobs := q.Snapshot()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].State != Done {
		t.Fatalf("state = %v (%v)\n%s", jobs[0].State, jobs[0].Err, jobs[0].Output)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Errorf("clone missing: %v", err)
	}
	if jobs[0].Elapsed() <= 0 {
		t.Error("Elapsed() = 0, want the measured duration")
	}
}

func TestQueueRunsJobsConcurrentlyAndKeepsPickerResponsive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	origin := initOrigin(t)
	dir := t.TempDir()

	q := New(context.Background(), Options{Concurrency: 2})
	for _, name := range []string{"a", "b", "c"} {
		if _, err := q.Enqueue(repo(name), filepath.Join(dir, name), origin, nil); err != nil {
			t.Fatalf("Enqueue(%s) = %v", name, err)
		}
	}
	// Enqueue returns immediately: the caller (the picker) is never blocked.
	if total := q.Counts().Total(); total != 3 {
		t.Fatalf("queued %d, want 3", total)
	}

	q.Close()
	waitFor(t, "all clones to finish", func() bool { return q.Counts().Pending() == 0 })
	q.Wait()

	if c := q.Counts(); c.Done != 3 {
		t.Errorf("counts = %+v, want 3 done", c)
	}
}

func TestEnqueueRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	q := New(context.Background(), Options{Concurrency: 1, DryRun: true})
	defer func() { q.Close(); q.Wait() }()

	if _, err := q.Enqueue(repo("a"), filepath.Join(dir, "a"), "url", nil); err != nil {
		t.Fatalf("first Enqueue() = %v", err)
	}
	if _, err := q.Enqueue(repo("a"), filepath.Join(dir, "other"), "url", nil); err == nil {
		t.Error("re-queueing the same repository should be rejected")
	}
	if _, err := q.Enqueue(repo("b"), filepath.Join(dir, "a"), "url", nil); err == nil {
		t.Error("two jobs writing the same directory should be rejected")
	}
	if total := q.Counts().Total(); total != 1 {
		t.Errorf("jobs = %d, want 1", total)
	}
}

func TestEnqueueSkipsExistingDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "taken")
	if err := os.MkdirAll(filepath.Join(dest, "stuff"), 0o755); err != nil {
		t.Fatal(err)
	}

	q := New(context.Background(), Options{Concurrency: 1})
	defer func() { q.Close(); q.Wait() }()

	job, err := q.Enqueue(repo("taken"), dest, "url", nil)
	if err == nil {
		t.Fatal("want an error for an occupied destination")
	}
	if job.State != Skipped {
		t.Errorf("state = %v, want skipped", job.State)
	}
}

func TestDryRunDoesNotClone(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "clone")
	q := New(context.Background(), Options{Concurrency: 1, DryRun: true})
	job, err := q.Enqueue(repo("a"), dest, "https://example.invalid/a.git", []string{"--depth", "1"})
	if err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	q.Close()
	q.Wait()

	if job.State != Skipped {
		t.Errorf("state = %v, want skipped", job.State)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("dry run created the destination")
	}
	if got := job.Command(); !strings.Contains(got, "--depth 1") || !strings.Contains(got, dest) {
		t.Errorf("Command() = %q", got)
	}
}

func TestEnqueueAfterCloseFails(t *testing.T) {
	q := New(context.Background(), Options{Concurrency: 1, DryRun: true})
	q.Close()
	q.Wait()
	if _, err := q.Enqueue(repo("a"), filepath.Join(t.TempDir(), "a"), "url", nil); err == nil {
		t.Error("Enqueue after Close should fail")
	}
}

func TestFailedCloneIsReported(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dest := filepath.Join(t.TempDir(), "clone")
	q := New(context.Background(), Options{Concurrency: 1})
	if _, err := q.Enqueue(repo("nope"), dest, filepath.Join(t.TempDir(), "not-a-repo"), nil); err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	q.Close()
	q.Wait()

	job := q.Snapshot()[0]
	if job.State != Failed {
		t.Fatalf("state = %v, want failed", job.State)
	}
	if job.Err == nil {
		t.Error("Err = nil, want the git failure")
	}
	if job.Output == "" {
		t.Error("Output = empty, want git's message")
	}
}

func TestStateTerminal(t *testing.T) {
	for state, want := range map[State]bool{
		Queued: false, Running: false, Done: true, Failed: true, Skipped: true,
	} {
		if got := state.Terminal(); got != want {
			t.Errorf("%v.Terminal() = %v, want %v", state, got, want)
		}
	}
}

func TestTailWriterKeepsTail(t *testing.T) {
	w := &tailWriter{limit: 8}
	w.Write([]byte("0123456789abc"))
	if got := w.String(); got != "56789abc" {
		t.Errorf("String() = %q, want the last bytes", got)
	}
}

// initOrigin creates a tiny local repository that can be cloned by path.
func initOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "--quiet", "-m", "initial")
	return dir
}
