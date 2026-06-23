package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// --- planning flow messages ---

// planPreparedMsg is produced after the worktree is created and the spec is
// expanded; the root then launches the claude plan-mode pane.
type planPreparedMsg struct {
	taskID     int64
	title      string
	worktree   string
	expanded   string
	snapshot   map[string]bool
	setupLabel string
}

// planSessionStartedMsg notifies the planner that its request was launched.
type planSessionStartedMsg struct{ title string }

// planPollMsg drives best-effort detection of the produced plan file.
type planPollMsg struct{}

// paneHeight is the rows available to a fullscreen session pane (minus the
// session status bar).
func (m Model) paneHeight() int {
	h := m.height - 1
	if h < 1 {
		h = 1
	}
	return h
}

// preparePlanCmd creates the task + worktree and expands the spec, off the
// Update loop. It returns planPreparedMsg on success or errMsg on failure.
func (m Model) preparePlanCmd(req launchPlanRequestMsg) tea.Cmd {
	st := m.store
	wt := m.wt
	br := m.brain
	return func() tea.Msg {
		c := context.Background()
		title := firstLine(req.spec)

		task, err := st.CreateTask(c, store.Task{
			ProjectID: req.project.ID,
			Title:     title,
			Spec:      req.spec,
			Status:    store.StatusPlanning,
			AgentKind: store.AgentClaude,
		})
		if err != nil {
			return errMsg{err}
		}

		wtree, err := wt.Create(c, req.project.RepoPath, req.project.Name, title, req.project.DefaultBranch)
		if err != nil {
			return errMsg{err}
		}
		_ = st.UpdateTaskWorktree(c, task.ID, wtree.Path, wtree.Branch)

		// Fail-soft: ExpandSpec returns a usable prompt even on API error.
		expanded, _ := br.ExpandSpec(c, req.spec)

		return planPreparedMsg{
			taskID:     task.ID,
			title:      title,
			worktree:   wtree.Path,
			expanded:   expanded,
			snapshot:   snapshotPlans(plansDir()),
			setupLabel: wtree.SetupLabel,
		}
	}
}

// startPlanSession launches claude in plan mode inside the worktree and
// registers the session, attaching the view to it.
func (m *Model) startPlanSession(p planPreparedMsg) (tea.Model, tea.Cmd) {
	m.nextSessID++
	id := m.nextSessID

	pane, err := startAgentPane(id, p.worktree, claudePlanArgv(p.expanded), maxi(m.width, 1), maxi(m.paneHeight(), 1))
	if err != nil {
		m.err = err
		return *m, nil
	}
	pane.focused = true

	m.sessions[id] = &session{
		id:       id,
		taskID:   p.taskID,
		title:    p.title,
		kind:     "plan",
		pane:     pane,
		worktree: p.worktree,
		plansDir: plansDir(),
		snapshot: p.snapshot,
		launchAt: time.Now(),
	}
	m.viewing = id
	m.lastSession = id
	m.status = "planning: " + p.title

	return *m, tea.Batch(
		pane.Init(),
		m.pollCmd(),
		func() tea.Msg { return planSessionStartedMsg{title: p.title} },
		listTasksCmd(m.store),
	)
}

// handlePaneExited marks the session exited and, for plan sessions, ingests the
// produced plan file (best-effort) and moves the task to ready.
func (m *Model) handlePaneExited(msg paneExitedMsg) (tea.Model, tea.Cmd) {
	s := m.sessions[msg.id]
	if s == nil {
		return *m, nil
	}
	s.exited = true
	if s.kind == "plan" && !s.ingested {
		if s.planPath == "" {
			s.planPath = findNewPlan(s.plansDir, s.snapshot, s.launchAt)
		}
		_ = m.store.SetTaskPlanReady(ctx(), s.taskID, s.planPath)
		_ = m.store.AppendEvent(ctx(), "plan.ready", `{"task":`+strconv.FormatInt(s.taskID, 10)+`}`)
		s.ingested = true
		if s.planPath != "" {
			m.status = "plan ready: " + s.title
		} else {
			m.status = "plan session ended: " + s.title
		}
	}
	return *m, listTasksCmd(m.store)
}

// handlePlanPoll refreshes plan-file candidates for live plan sessions and
// re-arms the poll while any remain.
func (m *Model) handlePlanPoll() (tea.Model, tea.Cmd) {
	anyLive := false
	for _, s := range m.sessions {
		if s.kind == "plan" && !s.exited {
			anyLive = true
			if s.planPath == "" {
				if p := findNewPlan(s.plansDir, s.snapshot, s.launchAt); p != "" {
					s.planPath = p
				}
			}
		}
	}
	if anyLive {
		return *m, m.pollCmd()
	}
	return *m, nil
}

func (m Model) pollCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return planPollMsg{} })
}

// listTasksCmd reloads tasks and emits tasksLoadedMsg (broadcast refreshes the
// Dashboard and Tasks tabs).
func listTasksCmd(st *store.Store) tea.Cmd {
	return func() tea.Msg {
		ts, err := st.ListTasks(ctx())
		if err != nil {
			return errMsg{err}
		}
		return tasksLoadedMsg{ts}
	}
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 80 {
				line = line[:80]
			}
			return line
		}
	}
	return "untitled task"
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
