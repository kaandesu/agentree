package tui

import (
	"fmt"

	"agentree/internal/orchestrator"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// shipDoneMsg reports the outcome of a completion action.
type shipDoneMsg struct {
	sessionID int
	action    string
	url       string // PR URL on success of "pr"
	err       error
}

// handleDiffRequest computes the changed-file list for a session's worktree off
// the Update loop and broadcasts it to the dashboard.
func (m *Model) handleDiffRequest(sessionID int) (tea.Model, tea.Cmd) {
	s := m.sessions[sessionID]
	if s == nil || s.dir == "" {
		return *m, nil
	}
	worktree, base := s.dir, s.baseBranch
	return *m, func() tea.Msg {
		files, err := orchestrator.ChangedFiles(ctx(), worktree, base)
		if err != nil {
			return errMsg{err}
		}
		return diffLoadedMsg{sessionID: sessionID, files: files}
	}
}

// handleShipRequest opens a confirm overlay for a hard-to-reverse completion
// action; the carried command runs only on confirmation.
func (m *Model) handleShipRequest(sessionID int, action string) (tea.Model, tea.Cmd) {
	s := m.sessions[sessionID]
	if s == nil {
		m.status = "session not found"
		return *m, nil
	}
	var prompt, detail string
	switch action {
	case "pr":
		prompt = "Open a pull request?"
		detail = fmt.Sprintf("push %s to origin and run gh pr create --base %s", s.branch, s.baseBranch)
	case "merge":
		prompt = "Merge into " + s.baseBranch + "?"
		detail = fmt.Sprintf("git merge --no-ff %s (aborts on conflict)", s.branch)
	case "discard":
		prompt = "Discard this worktree?"
		detail = fmt.Sprintf("remove %s, delete branch %s, kill the tmux window", s.dir, s.branch)
	default:
		return *m, nil
	}
	cm := newConfirmModal(m.theme, prompt, detail, m.shipActionCmd(sessionID, action))
	cm.SetSize(m.width, m.height)
	m.overlay = &cm
	return *m, nil
}

// shipActionCmd performs a completion action against the shipper off the Update
// loop and returns shipDoneMsg.
func (m *Model) shipActionCmd(sessionID int, action string) tea.Cmd {
	s := m.sessions[sessionID]
	if s == nil {
		return nil
	}
	ship := m.ship
	tm := m.tmux
	repo, base, branch, worktree, winID := s.repoPath, s.baseBranch, s.branch, s.dir, s.windowID
	return func() tea.Msg {
		c := ctx()
		switch action {
		case "pr":
			if err := ship.Push(c, repo, branch); err != nil {
				return shipDoneMsg{sessionID: sessionID, action: action, err: err}
			}
			url, err := ship.OpenPR(c, repo, base, branch)
			return shipDoneMsg{sessionID: sessionID, action: action, url: url, err: err}
		case "merge":
			err := ship.Merge(c, repo, base, branch)
			return shipDoneMsg{sessionID: sessionID, action: action, err: err}
		case "discard":
			err := ship.Discard(c, repo, worktree, branch)
			_ = tm.KillWindow(c, winID) // best-effort window cleanup
			return shipDoneMsg{sessionID: sessionID, action: action, err: err}
		}
		return shipDoneMsg{sessionID: sessionID, action: action, err: fmt.Errorf("unknown action %q", action)}
	}
}

// taskRemovedMsg reports a task removal: the affected agent sessions are dropped
// from the session map (their tmux windows are already killed).
type taskRemovedMsg struct {
	taskID     int64
	sessionIDs []int
}

// handleRemoveTaskRequest opens a confirm overlay for removing a task. It gathers
// the task's live agent sessions up front (the root owns the session map) so the
// confirmed command kills exactly those windows — worktrees are left on disk.
func (m *Model) handleRemoveTaskRequest(task store.Task) (tea.Model, tea.Cmd) {
	var sids []int
	var winIDs []string
	for _, s := range m.sessions {
		if s.taskID == task.ID && s.kind == "agent" && !s.dead {
			sids = append(sids, s.id)
			winIDs = append(winIDs, s.windowID)
		}
	}
	detail := "mark the task discarded; it won’t run anymore"
	if len(winIDs) > 0 {
		detail = fmt.Sprintf("kill %d running agent(s) (worktrees kept) and mark the task discarded", len(winIDs))
	}
	cm := newConfirmModal(m.theme, "Remove this task?", detail, m.removeTaskCmd(task.ID, sids, winIDs))
	cm.SetSize(m.width, m.height)
	m.overlay = &cm
	return *m, nil
}

// removeTaskCmd kills the task's live agent windows (best-effort, worktrees left
// intact), marks the task discarded, and records the event.
func (m *Model) removeTaskCmd(taskID int64, sids []int, winIDs []string) tea.Cmd {
	st, tm := m.store, m.tmux
	return func() tea.Msg {
		c := ctx()
		for _, w := range winIDs {
			_ = tm.KillWindow(c, w) // best-effort; worktree left intact
		}
		_ = st.UpdateTaskStatus(c, taskID, store.StatusDiscarded)
		_ = st.Emit(c, store.EventTaskRemoved, map[string]any{"task": taskID, "agents": len(winIDs)})
		return taskRemovedMsg{taskID: taskID, sessionIDs: sids}
	}
}

// handleTaskRemoved drops the killed sessions from the map and refreshes tasks.
func (m *Model) handleTaskRemoved(msg taskRemovedMsg) (tea.Model, tea.Cmd) {
	for _, sid := range msg.sessionIDs {
		delete(m.sessions, sid) // stop tracking; tmux window already killed
	}
	m.status = "task removed"
	return *m, tea.Batch(m.ensurePolling(), listTasksCmd(m.store))
}

// soleAgentForTask reports whether exactly one agent session belongs to taskID.
func (m *Model) soleAgentForTask(taskID int64) bool {
	n := 0
	for _, s := range m.sessions {
		if s.kind == "agent" && s.taskID == taskID {
			n++
		}
	}
	return n == 1
}

// handleShipDone reconciles state after a completion action: records the event,
// updates the session map, and refreshes the dashboard.
func (m *Model) handleShipDone(msg shipDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err
		return *m, nil
	}
	st := m.store
	switch msg.action {
	case "pr":
		m.status = "PR opened: " + msg.url
		_ = st.Emit(ctx(), store.EventPROpened, map[string]any{"session": msg.sessionID, "url": msg.url})
	case "merge":
		m.status = "merged into base branch"
		_ = st.Emit(ctx(), store.EventMerged, map[string]any{"session": msg.sessionID})
		// Split agents share one plan task; only mark it done when this is the
		// sole agent for it, so merging one of N worktrees doesn't overstate
		// the task's completion on the Tasks list.
		if s := m.sessions[msg.sessionID]; s != nil && m.soleAgentForTask(s.taskID) {
			_ = st.UpdateTaskStatus(ctx(), s.taskID, store.StatusDone)
		}
	case "discard":
		m.status = "worktree discarded"
		_ = st.Emit(ctx(), store.EventDiscarded, map[string]any{"session": msg.sessionID})
		delete(m.sessions, msg.sessionID)
	}
	return *m, tea.Batch(m.ensurePolling(), listTasksCmd(m.store))
}
