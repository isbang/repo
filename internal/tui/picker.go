// Package tui implements the interactive fuzzy picker.
package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/isbang/repo/internal/cloner"
	"github.com/isbang/repo/internal/ghapi"
	"github.com/isbang/repo/internal/humanize"
	"github.com/isbang/repo/internal/query"
)

// ErrAborted reports that the user left the picker without queueing anything.
var ErrAborted = errors.New("cancelled")

const (
	// reloadInterval is how often the picker looks for a fresher cache written
	// by the background refresh, and repaints running clone progress.
	reloadInterval = 250 * time.Millisecond
	// jobRows is how many clone-queue lines are shown at the bottom.
	jobRows = 4
)

// Options configures the picker.
type Options struct {
	Repos     []ghapi.Repo
	FetchedAt time.Time
	// Query pre-fills the search box.
	Query string
	// Clones maps "owner/name" to a recent clone count, used for ranking.
	Clones map[string]int
	// Enqueue is called when the user presses enter. The returned message is
	// shown in the status line; ok reports whether the job was accepted.
	Enqueue func(ghapi.Repo) (msg string, ok bool)
	// Jobs returns the current clone queue, for the progress panel.
	Jobs func() []cloner.Job
	// Reload is polled for a newer cache; changed is false when nothing changed.
	Reload func() (repos []ghapi.Repo, fetchedAt time.Time, changed bool)
	// Refreshing reports whether a background refresh is running.
	Refreshing func() bool
	// Refresh triggers a background refresh (ctrl+r).
	Refresh func()
}

// Run shows the picker until the user quits. Enter queues the highlighted
// repository for cloning and leaves the picker open, so several repositories can
// be queued in one session.
//
// The UI is drawn on stderr so that callers can still pipe machine-readable
// output on stdout.
func Run(opts Options) error {
	// bubbles' own styles (the text input cursor) use the default lipgloss
	// renderer; point it at stderr too so they are not stripped when stdout is
	// piped.
	lipgloss.SetDefaultRenderer(renderer)

	p := tea.NewProgram(newModel(opts),
		tea.WithAltScreen(),
		tea.WithOutput(os.Stderr),
	)
	_, err := p.Run()
	return err
}

// renderer is bound to stderr because that is where the picker draws; the
// default lipgloss renderer would size up stdout instead, and disable colour
// whenever stdout is piped.
//
// The palette deliberately avoids lipgloss.AdaptiveColor: resolving it makes
// termenv query the terminal for its background colour and read the reply from
// stdin, which both slows startup and races with the user's first keystroke.
// These mid-range 256-colour values are legible on light and dark backgrounds.
var renderer = lipgloss.NewRenderer(os.Stderr)

var (
	promptStyle = renderer.NewStyle().Foreground(lipgloss.Color("170")).Bold(true)
	dimStyle    = renderer.NewStyle().Foreground(lipgloss.Color("244"))
	countStyle  = renderer.NewStyle().Foreground(lipgloss.Color("245"))
	pointer     = renderer.NewStyle().Foreground(lipgloss.Color("170")).Bold(true).Render("❯")
	nameStyle   = renderer.NewStyle()
	selName     = renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	hitStyle    = renderer.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	tagStyle    = renderer.NewStyle().Foreground(lipgloss.Color("104"))
	okStyle     = renderer.NewStyle().Foreground(lipgloss.Color("71"))
	warnStyle   = renderer.NewStyle().Foreground(lipgloss.Color("203"))
	busyStyle   = renderer.NewStyle().Foreground(lipgloss.Color("178"))
	helpStyle   = renderer.NewStyle().Foreground(lipgloss.Color("243"))
)

// spinnerFrames animates running clones.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type tickMsg time.Time

type model struct {
	opts  Options
	input textinput.Model

	repos     []ghapi.Repo
	results   []query.Result
	fetchedAt time.Time

	cursor int
	offset int
	width  int
	height int

	jobs     []cloner.Job
	jobState map[string]cloner.State
	spinner  int

	refreshing bool
	status     string
}

func newModel(opts Options) model {
	in := textinput.New()
	in.Prompt = promptStyle.Render("❯ ")
	in.Placeholder = "fuzzy search…"
	in.CharLimit = 200
	in.SetValue(opts.Query)
	in.CursorEnd()
	in.Focus()

	m := model{
		opts:      opts,
		input:     in,
		repos:     opts.Repos,
		fetchedAt: opts.FetchedAt,
		jobState:  map[string]cloner.State{},
		width:     80,
		height:    24,
	}
	m.filter("")
	if opts.Refreshing != nil {
		m.refreshing = opts.Refreshing()
	}
	m.syncJobs()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tick())
}

func tick() tea.Cmd {
	return tea.Tick(reloadInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Terminals that do not report a size (some CI pty setups) send zeroes;
		// keep the defaults rather than rendering into a 0-column window.
		if msg.Width > 0 {
			m.width = msg.Width
			m.input.Width = max(msg.Width-6, 10)
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.clampCursor()
		return m, nil

	case tickMsg:
		m.spinner++
		if m.opts.Refreshing != nil {
			m.refreshing = m.opts.Refreshing()
		}
		if m.opts.Reload != nil {
			if repos, fetchedAt, changed := m.opts.Reload(); changed {
				keep := m.selectedName()
				m.repos, m.fetchedAt = repos, fetchedAt
				m.filter(keep)
				m.status = fmt.Sprintf("list updated · %d repos", len(repos))
			}
		}
		m.syncJobs()
		return m, tick()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			m.enqueueSelected()
			return m, nil
		case "up", "ctrl+p", "ctrl+k":
			m.move(-1)
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			m.move(1)
			return m, nil
		case "pgup":
			m.move(-m.listHeight())
			return m, nil
		case "pgdown":
			m.move(m.listHeight())
			return m, nil
		case "home", "ctrl+home":
			m.cursor = 0
			m.clampCursor()
			return m, nil
		case "end":
			m.cursor = len(m.results) - 1
			m.clampCursor()
			return m, nil
		case "ctrl+r":
			if m.opts.Refresh != nil {
				m.opts.Refresh()
				m.refreshing = true
				m.status = "refresh requested"
			}
			return m, nil
		}
	}

	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.filter("")
		m.status = ""
	}
	return m, cmd
}

// enqueueSelected hands the highlighted repository to the clone queue and moves
// on to the next row, so several repositories can be queued in a row.
func (m *model) enqueueSelected() {
	if len(m.results) == 0 || m.opts.Enqueue == nil {
		return
	}
	repo := m.results[m.cursor].Repo
	msg, ok := m.opts.Enqueue(repo)
	m.status = msg
	m.syncJobs()
	if ok {
		m.move(1)
	}
}

// syncJobs refreshes the queue snapshot used by the panel and the row markers.
func (m *model) syncJobs() {
	if m.opts.Jobs == nil {
		return
	}
	m.jobs = m.opts.Jobs()
	m.jobState = make(map[string]cloner.State, len(m.jobs))
	for _, j := range m.jobs {
		m.jobState[j.Repo.FullName] = j.State
	}
}

// filter re-ranks the repositories for the current query. keep is the full name
// to leave selected if it survives the new filter.
func (m *model) filter(keep string) {
	m.results = query.Rank(m.input.Value(), m.repos, m.opts.Clones)
	m.cursor, m.offset = 0, 0
	if keep == "" {
		return
	}
	for i, r := range m.results {
		if r.Repo.FullName == keep {
			m.cursor = i
			break
		}
	}
	m.clampCursor()
}

func (m *model) move(delta int) {
	m.cursor += delta
	m.clampCursor()
}

func (m *model) clampCursor() {
	if len(m.results) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	m.cursor = min(max(m.cursor, 0), len(m.results)-1)

	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = min(max(m.offset, 0), max(len(m.results)-h, 0))
}

func (m model) selectedName() string {
	if len(m.results) == 0 {
		return ""
	}
	return m.results[m.cursor].Repo.FullName
}

// listHeight is the number of repository rows that fit: the window minus the
// prompt, the counter line, the clone panel and the help line.
func (m model) listHeight() int {
	return max(m.height-3-m.panelHeight(), 3)
}

// panelHeight is the number of lines the clone queue occupies (0 when empty).
func (m model) panelHeight() int {
	if len(m.jobs) == 0 {
		return 0
	}
	return 1 + min(len(m.jobs), jobRows) // summary + job lines
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")

	if len(m.results) == 0 {
		b.WriteString(dimStyle.Render("  no repository matches"))
		b.WriteString("\n")
	}

	h := m.listHeight()
	end := min(m.offset+h, len(m.results))
	nameCol := m.nameColumn(m.offset, end)
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(m.results[i], i == m.cursor, nameCol))
		b.WriteString("\n")
	}

	// Push the queue panel and help line to the bottom of the window.
	for i := end - m.offset; i < h; i++ {
		b.WriteString("\n")
	}
	if panel := m.renderPanel(); panel != "" {
		b.WriteString(panel)
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("enter queue clone · ctrl+r refresh · ↑↓/ctrl+p,n move · esc quit"))
	return b.String()
}

func (m model) statusLine() string {
	parts := []string{fmt.Sprintf("%d/%d", len(m.results), len(m.repos))}
	parts = append(parts, "synced "+humanize.Ago(m.fetchedAt))
	if m.refreshing {
		parts = append(parts, "refreshing…")
	}
	line := "  " + countStyle.Render(strings.Join(parts, " · "))
	if m.status != "" {
		line += "  " + tagStyle.Render(truncate(m.status, max(m.width-lipgloss.Width(line)-2, 10)))
	}
	return line
}

// nameColumn sizes the repository-name column to the widest visible name.
func (m model) nameColumn(start, end int) int {
	width := 24
	for i := start; i < end; i++ {
		if w := lipgloss.Width(m.results[i].Repo.FullName); w > width {
			width = w
		}
	}
	return min(width, max(m.width/2, 24))
}

func (m model) renderRow(res query.Result, selected bool, nameCol int) string {
	base := nameStyle
	if selected {
		base = selName
	}
	name := highlight(res.Repo.FullName, query.Positions(m.input.Value(), res.Repo.FullName), base, hitStyle, nameCol)

	prefix := "  "
	if selected {
		prefix = pointer + " "
	}
	// A fixed-width marker slot keeps the name column aligned whether or not a
	// repository has been queued.
	prefix += m.marker(res.Repo.FullName) + " "

	row := prefix + pad(name, lipgloss.Width(stripName(res.Repo.FullName, nameCol)), nameCol)
	if meta := m.metaOf(res); meta != "" {
		row += "  " + meta
	}
	if desc := res.Repo.Description; desc != "" {
		rest := m.width - lipgloss.Width(row) - 3
		if rest > 8 {
			row += "  " + dimStyle.Render(truncate(desc, rest))
		}
	}
	return row
}

// marker is the one-column clone state indicator shown next to a repository.
func (m model) marker(fullName string) string {
	state, ok := m.jobState[fullName]
	if !ok {
		return " "
	}
	return stateGlyph(state, m.spinner)
}

func stateGlyph(state cloner.State, frame int) string {
	switch state {
	case cloner.Queued:
		return dimStyle.Render("·")
	case cloner.Running:
		return busyStyle.Render(spinnerFrames[frame%len(spinnerFrames)])
	case cloner.Done:
		return okStyle.Render("✓")
	case cloner.Failed:
		return warnStyle.Render("✗")
	case cloner.Skipped:
		return dimStyle.Render("⊘")
	default:
		return " "
	}
}

func (m model) metaOf(res query.Result) string {
	var parts []string
	if res.Clones > 1 {
		parts = append(parts, dimStyle.Render(fmt.Sprintf("×%d", res.Clones)))
	}
	if res.Repo.Language != "" {
		parts = append(parts, tagStyle.Render(res.Repo.Language))
	}
	if res.Repo.Private {
		parts = append(parts, dimStyle.Render("private"))
	}
	if res.Repo.Fork {
		parts = append(parts, dimStyle.Render("fork"))
	}
	if res.Repo.Archived {
		parts = append(parts, warnStyle.Render("archived"))
	}
	return strings.Join(parts, " ")
}

// renderPanel draws the clone queue: a summary line plus the most interesting
// jobs (running first, then waiting, then the most recent to finish).
func (m model) renderPanel() string {
	if len(m.jobs) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "  "+countStyle.Render(m.queueSummary()))
	for _, job := range m.panelJobs() {
		lines = append(lines, "  "+m.renderJob(job))
	}
	return strings.Join(lines, "\n")
}

func (m model) queueSummary() string {
	var counts cloner.Counts
	for _, j := range m.jobs {
		switch j.State {
		case cloner.Queued:
			counts.Queued++
		case cloner.Running:
			counts.Running++
		case cloner.Done:
			counts.Done++
		case cloner.Failed:
			counts.Failed++
		case cloner.Skipped:
			counts.Skipped++
		}
	}
	parts := []string{fmt.Sprintf("queue %d", counts.Total())}
	for _, p := range []struct {
		n     int
		label string
	}{
		{counts.Running, "cloning"},
		{counts.Queued, "waiting"},
		{counts.Done, "done"},
		{counts.Failed, "failed"},
		{counts.Skipped, "skipped"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.label))
		}
	}
	return strings.Join(parts, " · ")
}

// panelJobs picks which jobs to show: in-flight work first, then the tail of
// what has finished.
func (m model) panelJobs() []cloner.Job {
	var active, finished []cloner.Job
	for _, j := range m.jobs {
		if j.State.Terminal() {
			finished = append(finished, j)
			continue
		}
		active = append(active, j)
	}
	// Running before waiting.
	sortStable(active, func(a, b cloner.Job) bool { return a.State == cloner.Running && b.State != cloner.Running })

	out := active
	for i := len(finished) - 1; i >= 0 && len(out) < jobRows; i-- {
		out = append(out, finished[i])
	}
	if len(out) > jobRows {
		out = out[:jobRows]
	}
	return out
}

func (m model) renderJob(job cloner.Job) string {
	line := stateGlyph(job.State, m.spinner) + " " + job.Repo.FullName

	var detail string
	switch job.State {
	case cloner.Running:
		detail = job.Elapsed().Round(time.Second).String()
	case cloner.Done:
		detail = fmt.Sprintf("%s → %s", job.Elapsed().Round(100*time.Millisecond), job.Dest)
	case cloner.Failed, cloner.Skipped:
		if job.Err != nil {
			detail = job.Err.Error()
		}
	}
	if detail == "" {
		return line
	}
	rest := m.width - lipgloss.Width(line) - 4
	if rest < 8 {
		return line
	}
	return line + "  " + dimStyle.Render(truncate(detail, rest))
}

// sortStable is a tiny insertion sort: the job panel holds a handful of entries.
func sortStable(jobs []cloner.Job, less func(a, b cloner.Job) bool) {
	for i := 1; i < len(jobs); i++ {
		for j := i; j > 0 && less(jobs[j], jobs[j-1]); j-- {
			jobs[j], jobs[j-1] = jobs[j-1], jobs[j]
		}
	}
}

// highlight styles the matched runes of text, truncating to limit columns.
// Runs of matched and unmatched runes are styled in one go, so a row costs a
// handful of escape sequences rather than one per character.
func highlight(text string, positions []int, base, hit lipgloss.Style, limit int) string {
	plain := stripName(text, limit)
	runes := []rune(plain)
	// A truncated name ends in an ellipsis that is not part of the match.
	matchable := len(runes)
	if plain != text {
		matchable--
	}

	hits := make(map[int]struct{}, len(positions))
	for _, p := range positions {
		if p < matchable {
			hits[p] = struct{}{}
		}
	}

	var b strings.Builder
	for i := 0; i < len(runes); {
		_, isHit := hits[i]
		j := i + 1
		for j < len(runes) {
			if _, h := hits[j]; h != isHit {
				break
			}
			j++
		}
		segment := string(runes[i:j])
		if isHit {
			b.WriteString(hit.Render(segment))
		} else {
			b.WriteString(base.Render(segment))
		}
		i = j
	}
	return b.String()
}

// stripName truncates a repository name to limit columns.
func stripName(name string, limit int) string {
	if lipgloss.Width(name) <= limit {
		return name
	}
	return truncate(name, limit)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	var (
		b    strings.Builder
		used int
	)
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteString("…")
	return b.String()
}

// pad right-pads a pre-styled string whose visible width is known.
func pad(styled string, visibleWidth, target int) string {
	if visibleWidth >= target {
		return styled
	}
	return styled + strings.Repeat(" ", target-visibleWidth)
}
