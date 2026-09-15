// Package cloner runs git clones in the background so the picker can stay open
// while repositories are being fetched.
package cloner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/gitx"
)

// State is where a job is in its life cycle.
type State int

const (
	Queued State = iota
	Running
	Done
	Failed
	// Skipped means the clone was not attempted: the destination is already
	// there, or this was a dry run.
	Skipped
)

func (s State) String() string {
	switch s {
	case Queued:
		return "queued"
	case Running:
		return "cloning"
	case Done:
		return "done"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// Terminal reports whether the state can still change.
func (s State) Terminal() bool { return s == Done || s == Failed || s == Skipped }

// Job is one queued clone. Snapshot returns copies, so the UI can read them
// without locking.
type Job struct {
	ID    int
	Repo  ghapi.Repo
	Dest  string
	URL   string
	Args  []string
	State State
	// Err is set for Failed, and carries the reason for Skipped.
	Err error
	// Output is the tail of git's output, kept for failures.
	Output     string
	QueuedAt   time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Elapsed is how long the job has been running, or took to run.
func (j Job) Elapsed() time.Duration {
	if j.StartedAt.IsZero() {
		return 0
	}
	if j.FinishedAt.IsZero() {
		return time.Since(j.StartedAt)
	}
	return j.FinishedAt.Sub(j.StartedAt)
}

// Command renders the git invocation, for dry runs and error reports.
func (j Job) Command() string {
	argv := append([]string{"git", "clone"}, j.Args...)
	argv = append(argv, "--", j.URL, j.Dest)
	return strings.Join(argv, " ")
}

// DefaultConcurrency is how many clones run at once by default. Clones are
// network-bound, and a handful in parallel is plenty for a picker session.
const DefaultConcurrency = 2

// queueCapacity bounds how many jobs can be waiting; far more than anyone
// queues by hand in one session.
const queueCapacity = 512

// Options configures a Queue.
type Options struct {
	// Concurrency is the number of parallel clones (default DefaultConcurrency).
	Concurrency int
	// DryRun records what would run without running it.
	DryRun bool
}

// Queue runs clones on a small pool of workers.
type Queue struct {
	ctx    context.Context
	dryRun bool

	mu     sync.Mutex
	jobs   []*Job
	dests  map[string]*Job
	repos  map[string]*Job
	closed bool
	nextID int

	ch chan *Job
	wg sync.WaitGroup
}

// New starts a queue and its workers. Cancelling ctx aborts running clones.
func New(ctx context.Context, opts Options) *Queue {
	workers := opts.Concurrency
	if workers < 1 {
		workers = DefaultConcurrency
	}
	q := &Queue{
		ctx:    ctx,
		dryRun: opts.DryRun,
		dests:  map[string]*Job{},
		repos:  map[string]*Job{},
		ch:     make(chan *Job, queueCapacity),
	}
	q.wg.Add(workers)
	for range workers {
		go q.worker()
	}
	return q
}

// ErrClosed is returned by Enqueue after Close.
var ErrClosed = errors.New("clone queue is closed")

// Enqueue adds a clone of repo into dest. It rejects a repository that is
// already queued and a destination another job has taken, and records an
// unusable destination as a skipped job so it stays visible.
func (q *Queue) Enqueue(repo ghapi.Repo, dest, url string, args []string) (*Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil, ErrClosed
	}
	if prev, ok := q.repos[repo.FullName]; ok {
		return prev, fmt.Errorf("%s is already %s", repo.FullName, prev.State)
	}
	if prev, ok := q.dests[dest]; ok {
		return prev, fmt.Errorf("%s is already queued for %s", prev.Repo.FullName, dest)
	}

	q.nextID++
	job := &Job{
		ID:       q.nextID,
		Repo:     repo,
		Dest:     dest,
		URL:      url,
		Args:     args,
		State:    Queued,
		QueuedAt: time.Now(),
	}
	q.jobs = append(q.jobs, job)
	q.dests[dest] = job
	q.repos[repo.FullName] = job

	// Reject an unusable destination up front: the user finds out while the
	// picker is still open instead of at the end.
	if err := gitx.CheckDest(dest); err != nil {
		job.State = Skipped
		job.Err = err
		job.FinishedAt = time.Now()
		return job, err
	}
	if q.dryRun {
		job.State = Skipped
		job.Err = errors.New("dry run")
		job.FinishedAt = time.Now()
		return job, nil
	}

	select {
	case q.ch <- job:
		return job, nil
	default:
		job.State = Failed
		job.Err = errors.New("clone queue is full")
		job.FinishedAt = time.Now()
		return job, job.Err
	}
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for job := range q.ch {
		q.run(job)
	}
}

func (q *Queue) run(job *Job) {
	q.mu.Lock()
	job.State = Running
	job.StartedAt = time.Now()
	q.mu.Unlock()

	out := &tailWriter{limit: 4 << 10}
	err := gitx.Clone(q.ctx, gitx.Options{
		URL:    job.URL,
		Dest:   job.Dest,
		Args:   job.Args,
		Stdout: out,
		Stderr: out,
		// Nothing is watching the terminal, so fail instead of hanging on a
		// credential prompt.
		NoPrompt: true,
	})

	q.mu.Lock()
	defer q.mu.Unlock()
	job.Output = out.String()
	job.FinishedAt = time.Now()
	if err != nil {
		job.State = Failed
		job.Err = err
		return
	}
	job.State = Done
}

// Snapshot returns a copy of every job, in the order they were queued.
func (q *Queue) Snapshot() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()

	out := make([]Job, len(q.jobs))
	for i, j := range q.jobs {
		out[i] = *j
	}
	return out
}

// Counts summarises the queue by state.
type Counts struct {
	Queued, Running, Done, Failed, Skipped int
}

// Total is the number of jobs ever queued.
func (c Counts) Total() int {
	return c.Queued + c.Running + c.Done + c.Failed + c.Skipped
}

// Pending is the number of jobs still to finish.
func (c Counts) Pending() int { return c.Queued + c.Running }

// Counts returns the current state tally.
func (q *Queue) Counts() Counts {
	q.mu.Lock()
	defer q.mu.Unlock()

	var c Counts
	for _, j := range q.jobs {
		switch j.State {
		case Queued:
			c.Queued++
		case Running:
			c.Running++
		case Done:
			c.Done++
		case Failed:
			c.Failed++
		case Skipped:
			c.Skipped++
		}
	}
	return c
}

// Close stops accepting jobs. Already queued jobs still run; use Wait to block
// until they finish.
func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.mu.Unlock()
	close(q.ch)
}

// Wait blocks until every queued job has finished. Close must be called first.
func (q *Queue) Wait() { q.wg.Wait() }

// tailWriter keeps only the last limit bytes written to it.
type tailWriter struct {
	limit int
	buf   []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if extra := len(w.buf) - w.limit; extra > 0 {
		w.buf = w.buf[extra:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return strings.TrimSpace(string(w.buf)) }
