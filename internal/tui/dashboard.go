package tui

import (
	"fmt"
	"strings"
	"time"

	"agentree/internal/orchestrator"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashboard shows the plan→agents session tree, the task backlog, and the
// supervisor's proactive suggestions. Agents run in a dedicated tmux server;
// the user presses ctrl+o to attach. A cursor selects a finished agent to
// review its diff and ship it (PR / merge / discard).
type dashboard struct {
	store     *store.Store
	theme     Theme
	inherited bool // agentree runs as a tmux pane (agents are sibling panes)
	w, h      int

	tasks       []store.Task
	loaded      bool
	windows     []windowView
	attach      string
	suggestions []suggestion

	cursor int                             // index into selectable agent rows
	diffs  map[int][]orchestrator.FileStat // sessionID -> changed files
	order  []int                           // sessionIDs of selectable agent rows
}

func newDashboard(st *store.Store, th Theme, inherited bool) *dashboard {
	return &dashboard{store: st, theme: th, inherited: inherited, diffs: map[int][]orchestrator.FileStat{}}
}

// tasksLoadedMsg carries a refreshed task list.
type tasksLoadedMsg struct{ tasks []store.Task }

// diffRequestMsg / shipRequestMsg are intents the dashboard emits; the root
// (which owns sessions + the shipper) executes them.
type diffRequestMsg struct{ sessionID int }
type shipRequestMsg struct {
	sessionID int
	action    string // "pr" | "merge" | "discard"
}

// diffLoadedMsg carries a computed changed-file list back to the dashboard.
type diffLoadedMsg struct {
	sessionID int
	files     []orchestrator.FileStat
}

func (d *dashboard) Init() tea.Cmd { return d.refresh() }

func (d *dashboard) refresh() tea.Cmd {
	return func() tea.Msg {
		ts, err := d.store.ListTasks(ctx())
		if err != nil {
			return errMsg{err}
		}
		return tasksLoadedMsg{ts}
	}
}

func (d *dashboard) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case tasksLoadedMsg:
		d.tasks = msg.tasks
		d.loaded = true
	case windowsMsg:
		d.windows = msg.windows
		d.attach = msg.attach
		d.rebuildOrder()
	case suggestionsMsg:
		d.suggestions = msg.suggestions
	case diffLoadedMsg:
		d.diffs[msg.sessionID] = msg.files
	case tea.KeyMsg:
		return d.handleKey(msg)
	}
	return d, nil
}

// rebuildOrder recomputes the selectable agent rows (and clamps the cursor)
// whenever the window list changes.
func (d *dashboard) rebuildOrder() {
	d.order = d.order[:0]
	for _, w := range d.windows {
		if w.kind == "agent" {
			d.order = append(d.order, w.sessionID)
		}
	}
	if d.cursor >= len(d.order) {
		d.cursor = len(d.order) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

func (d *dashboard) handleKey(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "r":
		return d, d.refresh()
	case "j", "down":
		if d.cursor < len(d.order)-1 {
			d.cursor++
		}
	case "k", "up":
		if d.cursor > 0 {
			d.cursor--
		}
	case "d":
		// Diffing a live agent is useful (progress check), so it's not gated.
		if sid, ok := d.selectedSessionID(); ok {
			return d, func() tea.Msg { return diffRequestMsg{sessionID: sid} }
		}
	case "p":
		return d, d.shipSelected("pr")
	case "m":
		return d, d.shipSelected("merge")
	case "x":
		return d, d.shipSelected("discard")
	}
	return d, nil
}

// shipSelected emits a ship intent only for a finished (dead) agent — PR/merge/
// discard on a still-running worktree would act on half-built work.
func (d *dashboard) shipSelected(action string) tea.Cmd {
	w, ok := d.selectedWindow()
	if !ok {
		return nil
	}
	if !w.dead {
		return func() tea.Msg { return statusMsg{text: "agent still running — wait for it to finish"} }
	}
	sid := w.sessionID
	return func() tea.Msg { return shipRequestMsg{sessionID: sid, action: action} }
}

func (d *dashboard) selectedSessionID() (int, bool) {
	if d.cursor < 0 || d.cursor >= len(d.order) {
		return 0, false
	}
	return d.order[d.cursor], true
}

func (d *dashboard) selectedWindow() (windowView, bool) {
	sid, ok := d.selectedSessionID()
	if !ok {
		return windowView{}, false
	}
	for _, w := range d.windows {
		if w.sessionID == sid {
			return w, true
		}
	}
	return windowView{}, false
}

func (d *dashboard) SetSize(w, h int) { d.w, d.h = w, h }

func (d *dashboard) View() string {
	var b strings.Builder

	// --- Suggestions (supervisor) ---
	if len(d.suggestions) > 0 {
		b.WriteString(d.theme.Title.Render("Suggestions"))
		b.WriteString("\n")
		for _, s := range d.suggestions {
			b.WriteString("  " + d.theme.StatusWarn.Render("›") + " " + s.text)
			if s.hint != "" {
				b.WriteString("  " + d.theme.Help.Render("("+s.hint+")"))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// --- Session tree ---
	b.WriteString(d.theme.Title.Render("Sessions"))
	b.WriteString("\n\n")
	if len(d.windows) == 0 {
		b.WriteString(d.theme.Subtle.Render("No sessions. Press 2 for the Planner to launch one, or ctrl+n to capture an idea."))
	} else {
		b.WriteString(d.renderTree())
		b.WriteString("\n")
		focusHelp := "ctrl+o: attach"
		if d.inherited {
			focusHelp = "ctrl+o / ctrl+b ←→: focus a pane"
		}
		b.WriteString(d.theme.Help.Render("j/k: select agent · d: diff · p: PR · m: merge · x: discard · " + focusHelp))
		if d.attach != "" {
			b.WriteString("\n" + d.theme.Subtle.Render("attach elsewhere: "+d.attach))
		}
	}

	// --- Tasks ---
	b.WriteString("\n\n")
	b.WriteString(d.theme.Title.Render("Tasks"))
	b.WriteString("\n\n")
	if !d.loaded {
		b.WriteString(d.theme.Subtle.Render("loading…"))
	} else if len(d.tasks) == 0 {
		b.WriteString(d.theme.Subtle.Render("No tasks yet. Press 2 to open the Planner, or ctrl+n to capture an idea."))
	} else {
		for _, t := range d.tasks {
			b.WriteString(fmt.Sprintf("%s  %-10s  %s\n", statusDot(d.theme, t.Status), t.Status, t.Title))
		}
	}
	b.WriteString("\n" + d.theme.Help.Render("r: refresh tasks"))
	return lipgloss.NewStyle().Width(d.w).Height(d.h).Padding(1, 2).Render(b.String())
}

// renderTree draws plan sessions as roots with their child agents indented.
func (d *dashboard) renderTree() string {
	children := map[int][]windowView{}
	var roots []windowView
	for _, w := range d.windows {
		if w.kind == "plan" {
			roots = append(roots, w)
		}
	}
	for _, w := range d.windows {
		if w.kind == "agent" {
			children[w.parentID] = append(children[w.parentID], w)
		}
	}

	var b strings.Builder
	emitAgent := func(w windowView, last bool) {
		branch := "├─"
		if last {
			branch = "└─"
		}
		cursor := "  "
		if sid, ok := d.selectedSessionID(); ok && sid == w.sessionID {
			cursor = d.theme.Accent.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%s %s %s %s\n",
			cursor, d.theme.Tree.Render(branch), d.agentDot(w), w.title, d.theme.Subtle.Render(d.meta(w))))
		// Show the changed-file list under the selected agent once loaded.
		if sid, ok := d.selectedSessionID(); ok && sid == w.sessionID {
			b.WriteString(d.renderDiff(w))
		}
	}

	for _, r := range roots {
		b.WriteString(fmt.Sprintf("%s %s %s\n", d.agentDot(r), r.title, d.theme.Subtle.Render(d.meta(r))))
		kids := children[r.sessionID]
		for i, k := range kids {
			emitAgent(k, i == len(kids)-1)
		}
	}
	// Orphan agents (parent not a known plan session) render flat.
	for _, w := range d.windows {
		if w.kind != "agent" {
			continue
		}
		if !hasRoot(roots, w.parentID) {
			emitAgent(w, true)
		}
	}
	return b.String()
}

func hasRoot(roots []windowView, id int) bool {
	for _, r := range roots {
		if r.sessionID == id {
			return true
		}
	}
	return false
}

// meta renders elapsed + activity/exit info for a session row.
func (d *dashboard) meta(w windowView) string {
	parts := []string{}
	if w.elapsed > 0 {
		parts = append(parts, formatDuration(w.elapsed))
	}
	if w.dead {
		if w.status != 0 {
			parts = append(parts, fmt.Sprintf("exit %d", w.status))
		} else {
			parts = append(parts, "exited")
		}
	} else if !w.lastActivity.IsZero() {
		idle := time.Since(w.lastActivity)
		if idle > 30*time.Second {
			parts = append(parts, "idle "+formatDuration(idle))
		} else {
			parts = append(parts, "active")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "· " + strings.Join(parts, " · ")
}

func (d *dashboard) renderDiff(w windowView) string {
	files, ok := d.diffs[w.sessionID]
	if !ok {
		return "     " + d.theme.Help.Render("press d to load diff") + "\n"
	}
	if len(files) == 0 {
		return "     " + d.theme.Subtle.Render("no changes vs "+w.base) + "\n"
	}
	var b strings.Builder
	for _, f := range files {
		counts := ""
		if f.Additions >= 0 || f.Deletions >= 0 {
			counts = fmt.Sprintf("  %s %s",
				d.theme.StatusOk.Render(fmt.Sprintf("+%d", f.Additions)),
				d.theme.StatusErr.Render(fmt.Sprintf("-%d", f.Deletions)))
		}
		b.WriteString(fmt.Sprintf("       %s %s%s\n", d.theme.Subtle.Render(f.Status), f.Path, counts))
	}
	return b.String()
}

// agentDot colors a session's status via the central theme.
func (d *dashboard) agentDot(w windowView) string {
	if !w.dead {
		return d.theme.StatusOk.Render("●")
	}
	if w.status != 0 {
		return d.theme.StatusErr.Render("●")
	}
	return d.theme.StatusIdle.Render("●")
}

func statusDot(th Theme, s store.TaskStatus) string {
	switch s {
	case store.StatusRunning:
		return th.StatusOk.Render("●")
	case store.StatusReady, store.StatusReview:
		return th.StatusWarn.Render("●")
	case store.StatusDone:
		return th.StatusIdle.Render("●")
	default:
		return th.StatusIdle.Render("○")
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// errMsg is a generic error carrier used by tab commands.
type errMsg struct{ err error }

// CapturingInput implements tab; the dashboard uses only letter keys (j/k/d/p/
// m/x/r), so it leaves digit tab-switching and q-quit global.
func (d *dashboard) CapturingInput() bool { return false }
