// Package tui implements the agentree terminal UI: a tabbed Bubbletea app that
// orchestrates worktrees and agents. The root Model routes input to the active
// tab and renders the chrome (tab bar + status bar). A global idea-capture
// modal can overlay any tab.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"agentree/internal/brain"
	"agentree/internal/config"
	"agentree/internal/orchestrator"
	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// newTmuxManager selects the tmux driver. Normally agentree runs inside tmux (it
// re-execs itself there at startup), so agents are spawned as panes beside the
// dashboard. AGENTREE_OWNS_TMUX, set by that bootstrap, marks a server we
// created (safe to set session-global options). When tmux can't be entered
// (absent / non-TTY) we fall back to the standalone one-window-per-agent server.
func newTmuxManager(cfg *config.Config) *orchestrator.TmuxManager {
	if os.Getenv("TMUX") != "" {
		return orchestrator.NewInheritedTmuxManager(os.Getenv("AGENTREE_OWNS_TMUX") == "1")
	}
	return orchestrator.NewTmuxManager(cfg.TmuxSocket)
}

type tabID int

const (
	tabDashboard tabID = iota
	tabPlanner
	tabProjects
	tabIdeas
	tabTasks
	numTabs
)

var tabTitles = map[tabID]string{
	tabDashboard: "Dashboard",
	tabPlanner:   "Planner",
	tabProjects:  "Projects",
	tabIdeas:     "Ideas",
	tabTasks:     "Tasks",
}

// tab is the contract every tab model implements. Tabs receive sizing and
// render into the content area between the tab bar and status bar.
type tab interface {
	Init() tea.Cmd
	Update(tea.Msg) (tab, tea.Cmd)
	View() string
	SetSize(w, h int)
	// CapturingInput reports whether the tab is actively editing text, so the
	// root should forward letter/number keys to it instead of treating them as
	// global shortcuts. Tab navigation, Ctrl+N and Ctrl+C remain global.
	CapturingInput() bool
}

// Model is the root application model.
type Model struct {
	cfg   *config.Config
	store *store.Store
	brain brain.Brain
	wt    *orchestrator.WorktreeManager
	tmux  *orchestrator.TmuxManager
	ship  orchestrator.Shipper
	keys  KeyMap
	theme Theme

	width, height int
	active        tabID
	tabs          [numTabs]tab

	// overlay covers the active tab when non-nil (idea capture, confirm prompt,
	// help). It owns all input until it reports done.
	overlay overlay

	// sessions are running plan/agent tmux windows, keyed by id. lastSession is
	// the most recent session; ctrl+o attaches to the tmux server (landing on
	// it) so the user can interact and tab-cycle between agents.
	sessions    map[int]*session
	lastSession int
	nextSessID  int
	// polling is true while a single planPollMsg tick-loop is live, so the
	// several code paths that want polling don't spawn parallel loops.
	polling bool
	// superviseTick counts poll ticks so the supervisor runs every few ticks
	// (its git diffs are heavier than a tmux list).
	superviseTick int

	status string
	err    error
}

// New constructs the root model with all tabs wired to shared state.
func New(cfg *config.Config, st *store.Store) Model {
	theme := NewTheme()
	m := Model{
		cfg:      cfg,
		store:    st,
		brain:    brain.New(brain.Provider(cfg.BrainProvider), cfg.BrainModel, cfg.APIKey()),
		wt:       orchestrator.NewWorktreeManager(cfg.WorktreeRoot),
		tmux:     newTmuxManager(cfg),
		ship:     orchestrator.NewGitShipper(),
		keys:     DefaultKeyMap(),
		theme:    theme,
		active:   tabDashboard,
		sessions: map[int]*session{},
		status:   "ready",
	}
	// Discover agentree's own tmux window up front (inherited mode) so agent
	// panes can be split into it without a per-flow Ensure race. Best-effort:
	// flows call Ensure again (idempotent) and surface any real error there.
	if m.tmux.Inherited() {
		_ = m.tmux.Ensure(context.Background())
	}
	m.tabs[tabDashboard] = newDashboard(st, theme, m.tmux.Inherited())
	m.tabs[tabPlanner] = newPlanner(st, theme)
	m.tabs[tabProjects] = newProjects(st, theme)
	m.tabs[tabIdeas] = newIdeas(st, theme)
	m.tabs[tabTasks] = newTasks(st, theme)
	return m
}

// WithInitialProject focuses the Planner on a project at startup (used when
// agentree is launched with a repo path argument, e.g. `agentree .`).
func (m Model) WithInitialProject(p store.Project) Model {
	m.active = tabPlanner
	if pl, ok := m.tabs[tabPlanner].(*planner); ok {
		pl.selectProject(p)
	}
	m.status = "project: " + p.Name
	return m
}

// promoteIdea opens the Planner prefilled from an idea, marks the idea promoted,
// and refreshes the Ideas tab. The eventual task carries the idea id (threaded
// through launchPlanRequestMsg).
func (m *Model) promoteIdea(idea store.Idea) (tea.Model, tea.Cmd) {
	if pl, ok := m.tabs[tabPlanner].(*planner); ok {
		pl.prefill(idea)
	}
	m.active = tabPlanner
	m.status = "promoted idea to Planner: " + idea.Title
	st := m.store
	id := idea.ID
	mark := func() tea.Msg {
		_ = st.SetIdeaStatus(ctx(), id, "promoted")
		items, err := st.ListIdeas(ctx())
		if err != nil {
			return errMsg{err}
		}
		return ideasLoadedMsg{items}
	}
	return *m, mark
}

// Init kicks off each tab's initial commands.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tabs {
		if c := t.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

// contentHeight is the rows available to the active tab (minus chrome).
func (m Model) contentHeight() int {
	const tabBar, statusBar = 1, 1
	h := m.height - tabBar - statusBar
	if h < 0 {
		h = 0
	}
	return h
}

func (m *Model) resizeTabs() {
	for i := range m.tabs {
		if m.tabs[i] != nil {
			m.tabs[i].SetSize(m.width, m.contentHeight())
		}
	}
}

// Update handles global keys first, then delegates to the modal or active tab.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		m.resizeTabs()
		if m.overlay != nil {
			m.overlay.SetSize(m.width, m.height)
		}
		return m, nil
	}

	// An overlay, when open, owns every message (keys, async triage, spinner
	// ticks) except a hard quit, so its multi-phase flow isn't interrupted.
	if m.overlay != nil {
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		done, cmd := m.overlay.Update(msg)
		if done {
			m.overlay = nil
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Always-global keys (work even while a tab captures text).
		switch {
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case msg.String() == "ctrl+o":
			// Agents are live panes beside the dashboard. In tmux, ctrl+o moves
			// focus to the next agent pane; in the standalone fallback it hands
			// the terminal to tmux to attach.
			return m, m.focusOrAttachCmd()
		case key.Matches(msg, m.keys.NextTab):
			m.active = (m.active + 1) % numTabs
			return m, nil
		case key.Matches(msg, m.keys.PrevTab):
			m.active = (m.active - 1 + numTabs) % numTabs
			return m, nil
		case key.Matches(msg, m.keys.CaptureIdea):
			mod := newIdeaModal(m.store, m.brain, m.theme)
			mod.SetSize(m.width, m.height)
			m.overlay = &mod
			return m, mod.Init()
		}

		// Letter/number shortcuts only when the active tab isn't editing text.
		if !m.tabs[m.active].CapturingInput() {
			switch {
			case key.Matches(msg, m.keys.Help):
				ho := newHelpOverlay(m.theme)
				ho.SetSize(m.width, m.height)
				m.overlay = &ho
				return m, nil
			case key.Matches(msg, m.keys.Quit):
				return m, tea.Quit
			case key.Matches(msg, m.keys.Tab1):
				m.active = tabDashboard
				return m, nil
			case key.Matches(msg, m.keys.Tab2):
				m.active = tabPlanner
				return m, nil
			case key.Matches(msg, m.keys.Tab3):
				m.active = tabProjects
				return m, nil
			case key.Matches(msg, m.keys.Tab4):
				m.active = tabIdeas
				return m, nil
			case key.Matches(msg, m.keys.Tab5):
				m.active = tabTasks
				return m, nil
			}
		}
	}

	// Root-level messages.
	switch msg := msg.(type) {
	case errMsg:
		m.err = msg.err
		return m, nil
	case ideaCapturedMsg:
		m.status = "idea filed"
	case statusMsg:
		m.status = msg.text
		return m, nil
	case promoteIdeaMsg:
		return (&m).promoteIdea(msg.idea)
	case launchPlanRequestMsg:
		if msg.mode == "split" {
			return (&m).startSplitNow(msg)
		}
		return m, m.preparePlanCmd(msg)
	case planPreparedMsg:
		return (&m).startPlanSession(msg)
	case planPollMsg:
		return (&m).handlePlanPoll()
	case planSplitMsg:
		return (&m).applyPlanSplit(msg)
	case diffRequestMsg:
		return (&m).handleDiffRequest(msg.sessionID)
	case shipRequestMsg:
		return (&m).handleShipRequest(msg.sessionID, msg.action)
	case shipDoneMsg:
		return (&m).handleShipDone(msg)
	case attachReturnedMsg:
		return (&m).handleAttachReturned()
	case windowsMsg:
		// Broadcast below so the dashboard can render the live window list.
	}

	// Key messages go only to the active tab; everything else (async loads,
	// captured-idea notifications) is broadcast so each tab can react.
	if _, isKey := msg.(tea.KeyMsg); isKey {
		updated, cmd := m.tabs[m.active].Update(msg)
		m.tabs[m.active] = updated
		return m, cmd
	}

	var cmds []tea.Cmd
	for i := range m.tabs {
		updated, cmd := m.tabs[i].Update(msg)
		m.tabs[i] = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// View renders tab bar + active tab content (or modal) + status bar. Live
// agents run in tmux; the user attaches (ctrl+o) to interact with them.
func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}

	tabBar := m.renderTabBar()

	var content string
	if m.overlay != nil {
		content = m.overlay.View()
	} else {
		content = m.tabs[m.active].View()
	}

	status := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, tabBar, content, status)
}

func (m Model) renderTabBar() string {
	var cells []string
	for id := tabID(0); id < numTabs; id++ {
		label := fmt.Sprintf("%d %s", int(id)+1, tabTitles[id])
		if id == m.active {
			cells = append(cells, m.theme.TabActive.Render(label))
		} else {
			cells = append(cells, m.theme.TabInactive.Render(label))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, cells...)
	return m.theme.TabBar.Width(m.width).Render(bar)
}

func (m Model) renderStatusBar() string {
	left := m.theme.Accent.Render("agentree")
	help := m.theme.Help.Render("tab: switch · ctrl+n: idea · q: quit · ?: help")
	msg := m.status
	if m.err != nil {
		msg = "error: " + m.err.Error()
	}
	if live := m.liveSessionCount(); live > 0 {
		msg = fmt.Sprintf("%s  %s", msg,
			m.theme.Accent.Render(fmt.Sprintf("◆ %d session(s) · ctrl+o attach", live)))
	}
	line := fmt.Sprintf(" %s  %s", left, msg)
	gap := m.width - lipgloss.Width(line) - lipgloss.Width(help) - 1
	if gap < 1 {
		gap = 1
	}
	return m.theme.StatusBar.Width(m.width).Render(line + strings.Repeat(" ", gap) + help)
}

// liveSessionCount counts sessions whose tmux window is still running.
func (m Model) liveSessionCount() int {
	n := 0
	for _, s := range m.sessions {
		if !s.dead {
			n++
		}
	}
	return n
}

// ctx is a convenience for tabs that need a context (short-lived ops).
func ctx() context.Context { return context.Background() }
