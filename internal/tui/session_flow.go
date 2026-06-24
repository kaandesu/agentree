package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentree/internal/orchestrator"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// --- planning flow messages ---

// planPollMsg drives periodic tmux window status checks.
type planPollMsg struct{}

// attachReturnedMsg fires when the user detaches from tmux (tea.ExecProcess
// callback).
type attachReturnedMsg struct{ err error }

// windowView is one session's status for the dashboard.
type windowView struct {
	sessionID    int
	parentID     int
	taskID       int64
	title        string
	kind         string // "plan" | "agent"
	dead         bool
	status       int // exit code when dead
	elapsed      time.Duration
	lastActivity time.Time
	repo         string
	worktree     string
	branch       string
	base         string
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
	branch   string
}

// planSplitMsg is the result of fanning a proposal out into parallel build
// agents. Reused by both the old and new flows for applyPlanSplit.
type planSplitMsg struct {
	planSessionID int
	planTaskID    int64
	planPath      string
	spawns        []agentSpawn
	projectID     int64
	projectName   string
	repoPath      string
	baseBranch    string
	err           error
}

// spawnFromProposal handles a confirmed proposal: creates the task, provisions
// worktrees, and launches claude build agents for each sub-task.
func (m *Model) spawnFromProposal(msg launchFromProposalMsg) (tea.Model, tea.Cmd) {
	if !orchestrator.TmuxAvailable() {
		m.err = fmt.Errorf("tmux not found on PATH — install tmux to run agents")
		return *m, nil
	}
	if err := m.tmux.Ensure(context.Background()); err != nil {
		m.err = err
		return *m, nil
	}
	total := msg.proposal.TotalAgents()
	m.status = fmt.Sprintf("spawning %d agent(s) across %d feature(s)…", total, len(msg.proposal.Features))
	return *m, tea.Batch(
		m.spawnProposalCmd(msg),
		func() tea.Msg { return planSessionStartedMsg{title: firstLine(msg.spec)} },
	)
}

// spawnProposalCmd creates the task and provisions worktrees + agents for each
// sub-task in the proposal. It returns a planSplitMsg so the existing
// applyPlanSplit pipeline registers the sessions.
func (m Model) spawnProposalCmd(msg launchFromProposalMsg) tea.Cmd {
	st := m.store
	tm := m.tmux
	wt := m.wt
	return func() tea.Msg {
		c := context.Background()
		title := firstLine(msg.spec)
		task, err := st.CreateTask(c, store.Task{
			ProjectID: msg.project.ID,
			IdeaID:    msg.ideaID,
			Title:     title,
			Spec:      msg.spec,
			Status:    store.StatusRunning,
			AgentKind: store.AgentClaude,
		})
		if err != nil {
			return errMsg{err}
		}

		var spawns []agentSpawn
		for _, feature := range msg.proposal.Features {
			for _, sub := range feature.SubTasks {
				wtree, err := wt.Create(c, msg.project.RepoPath, msg.project.Name, sub.Title, msg.project.DefaultBranch)
				if err != nil {
					continue
				}
				_ = st.Emit(c, store.EventWorktreeCreated, map[string]any{
					"task": task.ID, "feature": feature.Title, "title": sub.Title,
					"branch": wtree.Branch, "path": wtree.Path,
				})
				wid, err := tm.NewWindow(c, orchestrator.Slugify(sub.Title), wtree.Path, claudeBuildArgv(sub.Prompt))
				if err != nil {
					continue
				}
				spawns = append(spawns, agentSpawn{
					title: sub.Title, windowID: wid,
					worktree: wtree.Path, branch: wtree.Branch,
				})
			}
		}
		_ = st.Emit(c, store.EventPlanSplit, map[string]any{"task": task.ID, "agents": len(spawns)})
		var splitErr error
		if len(spawns) == 0 {
			splitErr = fmt.Errorf("could not create any worktree agents for this proposal")
		}
		return planSplitMsg{
			planSessionID: 0, planTaskID: task.ID, spawns: spawns,
			projectID: msg.project.ID, projectName: msg.project.Name,
			repoPath: msg.project.RepoPath, baseBranch: msg.project.DefaultBranch,
			err: splitErr,
		}
	}
}

// applyPlanSplit registers the spawned agents as sessions and advances the task.
func (m *Model) applyPlanSplit(msg planSplitMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && len(msg.spawns) == 0 {
		m.status = "split failed: " + msg.err.Error()
		return *m, nil
	}

	_ = m.store.UpdateTaskStatus(ctx(), msg.planTaskID, store.StatusRunning)

	for _, sp := range msg.spawns {
		m.nextSessID++
		id := m.nextSessID
		s := &session{
			id:          id,
			parentID:    msg.planSessionID,
			taskID:      msg.planTaskID,
			title:       sp.title,
			kind:        "agent",
			windowID:    sp.windowID,
			dir:         sp.worktree,
			branch:      sp.branch,
			launchAt:    time.Now(),
			projectID:   msg.projectID,
			projectName: msg.projectName,
			repoPath:    msg.repoPath,
			baseBranch:  msg.baseBranch,
		}
		m.sessions[id] = s
		m.lastSession = id
	}
	if m.tmux.Inherited() {
		m.status = fmt.Sprintf("split into %d parallel agent(s) — now building in panes on the right", len(msg.spawns))
	} else {
		m.status = fmt.Sprintf("split into %d parallel agent(s) — ctrl+o to attach & tab-cycle", len(msg.spawns))
	}
	poll := m.ensurePolling()
	return *m, tea.Batch(poll, listTasksCmd(m.store))
}

// handlePlanPoll refreshes tmux window state, publishes the window list to the
// dashboard, and re-arms while work remains.
func (m *Model) handlePlanPoll() (tea.Model, tea.Cmd) {
	c := context.Background()
	wins, _ := m.tmux.ListWindows(c)
	byID := map[string]orchestrator.Window{}
	for _, w := range wins {
		byID[w.ID] = w
	}

	var cmds []tea.Cmd
	pending := false
	agentDied := false
	for _, s := range m.sessions {
		w, ok := byID[s.windowID]
		if !ok || w.Dead {
			if !s.dead && s.kind == "agent" && !s.exitedReported {
				s.exitedReported = true
				agentDied = true
				code := 0
				if ok {
					code = w.Status
				}
				st := m.store
				title, branch, sid := s.title, s.branch, s.id
				cmds = append(cmds, func() tea.Msg {
					_ = st.Emit(ctx(), store.EventAgentExited, map[string]any{
						"session": sid, "title": title, "branch": branch, "code": code,
					})
					return nil
				})
			}
			s.dead = true
		}
		if s.kind == "agent" && !s.dead {
			pending = true
		}
	}

	var views []windowView
	for _, s := range m.sessions {
		w := byID[s.windowID]
		var elapsed time.Duration
		if !s.launchAt.IsZero() {
			elapsed = time.Since(s.launchAt)
		}
		views = append(views, windowView{
			sessionID:    s.id,
			parentID:     s.parentID,
			taskID:       s.taskID,
			title:        s.title,
			kind:         s.kind,
			dead:         s.dead || !ok(byID, s.windowID),
			status:       w.Status,
			elapsed:      elapsed,
			lastActivity: w.LastActivity,
			repo:         s.repoPath,
			worktree:     s.dir,
			branch:       s.branch,
			base:         s.baseBranch,
		})
	}
	cmds = append(cmds, func() tea.Msg {
		return windowsMsg{windows: views, attach: m.tmux.AttachCommand()}
	})
	m.superviseTick++
	if agentDied || m.superviseTick%3 == 0 {
		cmds = append(cmds, m.superviseCmd())
	}
	if pending {
		cmds = append(cmds, m.pollCmd())
	} else {
		m.polling = false
	}
	return *m, tea.Batch(cmds...)
}

func ok(m map[string]orchestrator.Window, id string) bool {
	_, found := m[id]
	return found
}

// handleAttachReturned fires after the user detaches from tmux.
func (m *Model) handleAttachReturned() (tea.Model, tea.Cmd) {
	return *m, m.ensurePolling()
}

// ensurePolling starts the periodic poll loop if one isn't already running.
func (m *Model) ensurePolling() tea.Cmd {
	if m.polling {
		return nil
	}
	m.polling = true
	return m.pollCmd()
}

// focusOrAttachCmd reacts to ctrl+o.
func (m Model) focusOrAttachCmd() tea.Cmd {
	if len(m.sessions) == 0 {
		return func() tea.Msg { return statusMsg{text: "no live agents yet"} }
	}
	if m.tmux.Inherited() {
		tm := m.tmux
		return func() tea.Msg {
			_ = tm.FocusNext(ctx())
			return nil
		}
	}
	target := ""
	if s := m.sessions[m.lastSession]; s != nil {
		target = s.windowID
	}
	c := m.tmux.AttachExecCmd(target)
	if c == nil {
		return nil
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return attachReturnedMsg{err: err}
	})
}

func (m Model) pollCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return planPollMsg{} })
}

// listTasksCmd reloads tasks and emits tasksLoadedMsg.
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
