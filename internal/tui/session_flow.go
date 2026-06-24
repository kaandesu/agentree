package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"agentree/internal/orchestrator"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// --- planning flow messages ---

// planPreparedMsg is produced after the spec is expanded; the root then opens
// the claude plan-mode tmux window. No worktree is created yet — planning runs
// read-only in the repo, and worktrees are provisioned lazily per sub-task once
// the plan is split.
type planPreparedMsg struct {
	taskID      int64
	title       string
	expanded    string
	snapshot    map[string]bool
	projectID   int64
	projectName string
	repoPath    string
	baseBranch  string
}

// planSessionStartedMsg notifies the planner that its request was launched.
type planSessionStartedMsg struct{ title string }

// planPollMsg drives periodic tmux window status + plan-file detection.
type planPollMsg struct{}

// attachReturnedMsg fires when the user detaches from tmux (tea.ExecProcess
// callback). On detach we opportunistically ingest+split any ready plan.
type attachReturnedMsg struct{ err error }

// windowView is one agent session's status for the dashboard.
type windowView struct {
	title  string
	kind   string // "plan" | "agent"
	dead   bool
	status int // exit code when dead
}

// windowsMsg carries a refreshed view of agentree's sessions for the dashboard.
type windowsMsg struct {
	windows []windowView
	attach  string
}

// agentSpawn is a worktree+window created for one split sub-task.
type agentSpawn struct {
	title    string
	windowID string
	worktree string
}

// planSplitMsg is the result of ingesting a plan and fanning it out into
// parallel build agents.
type planSplitMsg struct {
	planSessionID int
	planTaskID    int64
	planPath      string
	spawns        []agentSpawn
	err           error
}

// preparePlanCmd creates the task and expands the spec off the Update loop. It
// returns planPreparedMsg on success or errMsg on failure.
func (m Model) preparePlanCmd(req launchPlanRequestMsg) tea.Cmd {
	st := m.store
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

		// Fail-soft: ExpandSpec returns a usable prompt even on API error.
		expanded, _ := br.ExpandSpec(c, req.spec)

		return planPreparedMsg{
			taskID:      task.ID,
			title:       title,
			expanded:    expanded,
			snapshot:    snapshotPlans(plansDir()),
			projectID:   req.project.ID,
			projectName: req.project.Name,
			repoPath:    req.project.RepoPath,
			baseBranch:  req.project.DefaultBranch,
		}
	}
}

// startPlanSession opens claude plan mode in a tmux window inside the repo and
// registers the session. The user attaches to drive the interrogation; on
// detach (or claude exiting) the plan is auto-split into parallel agents.
func (m *Model) startPlanSession(p planPreparedMsg) (tea.Model, tea.Cmd) {
	if !orchestrator.TmuxAvailable() {
		m.err = fmt.Errorf("tmux not found on PATH — install tmux to run agents")
		return *m, nil
	}
	c := context.Background()
	if err := m.tmux.Ensure(c); err != nil {
		m.err = err
		return *m, nil
	}

	m.nextSessID++
	id := m.nextSessID
	wid, err := m.tmux.NewWindow(c, orchestrator.Slugify(p.title), p.repoPath, claudePlanArgv(p.expanded))
	if err != nil {
		m.err = err
		return *m, nil
	}

	m.sessions[id] = &session{
		id:          id,
		taskID:      p.taskID,
		title:       p.title,
		kind:        "plan",
		windowID:    wid,
		dir:         p.repoPath,
		projectID:   p.projectID,
		projectName: p.projectName,
		repoPath:    p.repoPath,
		baseBranch:  p.baseBranch,
		plansDir:    plansDir(),
		snapshot:    p.snapshot,
		launchAt:    time.Now(),
	}
	m.lastSession = id
	m.status = "planning: " + p.title + " — press ctrl+o to attach (answer claude, then ctrl+b d to detach & auto-split)"

	poll := m.ensurePolling()
	return *m, tea.Batch(
		poll,
		func() tea.Msg { return planSessionStartedMsg{title: p.title} },
		listTasksCmd(m.store),
	)
}

// ingestSplitCmd reads the plan a plan-session produced (file first, captured
// pane as fallback), splits it into parallel sub-tasks, and provisions a
// worktree + claude build window for each. It only kills the planning window
// once it actually has plan content, so an early detach (claude still asking
// questions) leaves the window alive for the user to re-attach.
func (m Model) ingestSplitCmd(s *session) tea.Cmd {
	st := m.store
	br := m.brain
	tm := m.tmux
	wt := m.wt

	sessID := s.id
	taskID := s.taskID
	winID := s.windowID
	planPath := s.planPath
	plans := s.plansDir
	snap := s.snapshot
	launchAt := s.launchAt
	repo := s.repoPath
	projName := s.projectName
	baseBranch := s.baseBranch

	return func() tea.Msg {
		c := context.Background()

		if planPath == "" {
			planPath = findNewPlan(plans, snap, launchAt)
		}
		var planText string
		if planPath != "" {
			if b, err := os.ReadFile(planPath); err == nil {
				planText = string(b)
			}
		}
		if strings.TrimSpace(planText) == "" {
			// Fall back to scraping the plan from the live window's output.
			planText, _ = tm.Capture(c, winID)
		}
		if strings.TrimSpace(planText) == "" {
			return planSplitMsg{planSessionID: sessID, planTaskID: taskID,
				err: fmt.Errorf("no plan content yet — attach and let claude present the plan")}
		}

		_ = st.SetTaskPlanReady(c, taskID, planPath)
		_ = st.AppendEvent(c, "plan.ready", `{"task":`+strconv.FormatInt(taskID, 10)+`}`)
		// Belt-and-suspenders: stop the planning agent so it can't start
		// building in the repo while we fan the work out into worktrees.
		_ = tm.KillWindow(c, winID)

		subs, _ := br.SplitPlan(c, planText)
		var spawns []agentSpawn
		for _, sub := range subs {
			wtree, err := wt.Create(c, repo, projName, sub.Title, baseBranch)
			if err != nil {
				continue
			}
			wid, err := tm.NewWindow(c, orchestrator.Slugify(sub.Title), wtree.Path, claudeBuildArgv(sub.Prompt))
			if err != nil {
				continue
			}
			spawns = append(spawns, agentSpawn{title: sub.Title, windowID: wid, worktree: wtree.Path})
		}
		return planSplitMsg{planSessionID: sessID, planTaskID: taskID, planPath: planPath, spawns: spawns}
	}
}

// applyPlanSplit registers the spawned agents as sessions and advances the task.
func (m *Model) applyPlanSplit(msg planSplitMsg) (tea.Model, tea.Cmd) {
	ps := m.sessions[msg.planSessionID]
	if msg.err != nil && len(msg.spawns) == 0 {
		// Nothing ingested (e.g. detached before the plan was ready). Leave the
		// plan session live so the user can re-attach and try again.
		if ps != nil {
			ps.splitting = false
		}
		m.status = msg.err.Error()
		return *m, nil
	}

	if ps != nil {
		ps.ingested = true
		ps.dead = true
		ps.splitting = false
	}
	_ = m.store.UpdateTaskStatus(ctx(), msg.planTaskID, store.StatusRunning)

	for _, sp := range msg.spawns {
		m.nextSessID++
		id := m.nextSessID
		m.sessions[id] = &session{
			id:       id,
			taskID:   msg.planTaskID,
			title:    sp.title,
			kind:     "agent",
			windowID: sp.windowID,
			dir:      sp.worktree,
		}
		m.lastSession = id
	}
	m.status = fmt.Sprintf("split into %d parallel agent(s) — ctrl+o to attach & tab-cycle", len(msg.spawns))
	poll := m.ensurePolling()
	return *m, tea.Batch(poll, listTasksCmd(m.store))
}

// handlePlanPoll refreshes tmux window state, auto-splits any plan whose window
// has died, publishes the window list to the dashboard, and re-arms while work
// remains.
func (m *Model) handlePlanPoll() (tea.Model, tea.Cmd) {
	c := context.Background()
	wins, _ := m.tmux.ListWindows(c)
	byID := map[string]orchestrator.Window{}
	for _, w := range wins {
		byID[w.ID] = w
	}

	var cmds []tea.Cmd
	pending := false
	for _, s := range m.sessions {
		w, ok := byID[s.windowID]
		if !ok || w.Dead {
			s.dead = true
		}
		if s.kind == "plan" && !s.ingested {
			if s.planPath == "" {
				if p := findNewPlan(s.plansDir, s.snapshot, s.launchAt); p != "" {
					s.planPath = p
				}
			}
			// claude exited on its own: ingest + split now.
			if s.dead && !s.splitting {
				s.splitting = true
				cmds = append(cmds, m.ingestSplitCmd(s))
			}
			pending = true
		}
		if s.kind == "agent" && !s.dead {
			pending = true
		}
	}

	var views []windowView
	for _, s := range m.sessions {
		w, ok := byID[s.windowID]
		views = append(views, windowView{
			title:  s.title,
			kind:   s.kind,
			dead:   s.dead || !ok,
			status: w.Status,
		})
	}
	cmds = append(cmds, func() tea.Msg {
		return windowsMsg{windows: views, attach: m.tmux.AttachCommand()}
	})
	// This tick consumed the live loop; re-arm only if work remains, otherwise
	// let the loop end (a starter re-arms it via ensurePolling later).
	if pending {
		cmds = append(cmds, m.pollCmd())
	} else {
		m.polling = false
	}
	return *m, tea.Batch(cmds...)
}

// handleAttachReturned fires after the user detaches from tmux. We try to
// ingest+split every not-yet-split plan session (no-op if no plan is ready).
func (m *Model) handleAttachReturned() (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, s := range m.sessions {
		if s.kind == "plan" && !s.ingested && !s.splitting {
			s.splitting = true
			cmds = append(cmds, m.ingestSplitCmd(s))
		}
	}
	cmds = append(cmds, m.ensurePolling())
	return *m, tea.Batch(cmds...)
}

// ensurePolling starts the periodic poll loop if one isn't already running,
// returning nil otherwise so callers never spawn parallel tick loops.
func (m *Model) ensurePolling() tea.Cmd {
	if m.polling {
		return nil
	}
	m.polling = true
	return m.pollCmd()
}

// attachCmd hands the terminal to tmux so the user can interact with agents.
// While attached, agentree's event loop is suspended; on detach we resume and
// reconcile state via attachReturnedMsg.
func (m Model) attachCmd() tea.Cmd {
	if len(m.sessions) == 0 {
		m.status = "no live sessions to attach to"
		return nil
	}
	target := ""
	if s := m.sessions[m.lastSession]; s != nil {
		target = s.windowID
	}
	c := m.tmux.AttachExecCmd(target)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return attachReturnedMsg{err: err}
	})
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
