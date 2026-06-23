// Package tui implements the agentree terminal UI: a tabbed Bubbletea app that
// orchestrates worktrees and agents. The root Model routes input to the active
// tab and renders the chrome (tab bar + status bar). A global idea-capture
// modal can overlay any tab.
package tui

import (
	"context"
	"fmt"
	"strings"

	"agentree/internal/brain"
	"agentree/internal/config"
	"agentree/internal/orchestrator"
	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
	// global shortcuts. Tab navigation, Ctrl+I and Ctrl+C remain global.
	CapturingInput() bool
}

// Model is the root application model.
type Model struct {
	cfg   *config.Config
	store *store.Store
	brain brain.Brain
	wt    *orchestrator.WorktreeManager
	keys  KeyMap
	theme Theme

	width, height int
	active        tabID
	tabs          [numTabs]tab

	// modal overlays the active tab when non-nil (e.g. idea capture).
	modal *ideaModal

	// sessions are running plan/agent panes, keyed by id. viewing is the
	// session shown fullscreen (0 = show tabs). lastSession is the most recent
	// session, used by ctrl+o to re-attach.
	sessions    map[int]*session
	viewing     int
	lastSession int
	nextSessID  int

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
		keys:     DefaultKeyMap(),
		theme:    theme,
		active:   tabDashboard,
		sessions: map[int]*session{},
		status:   "ready",
	}
	m.tabs[tabDashboard] = newDashboard(st, theme)
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeTabs()
		if m.modal != nil {
			m.modal.SetSize(m.width, m.height)
		}
		for _, s := range m.sessions {
			if s.pane != nil {
				s.pane.SetSize(m.width, m.paneHeight())
			}
		}
		return m, nil

	case tea.KeyMsg:
		// The modal, when open, captures all keys except a hard quit.
		if m.modal != nil {
			if key.Matches(msg, m.keys.Quit) && msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			var cmd tea.Cmd
			done, cmd := m.modal.Update(msg)
			if done {
				m.modal = nil
			}
			return m, cmd
		}

		// Session view: forward keys to the focused pane; ctrl+b detaches.
		if m.viewing != 0 {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "ctrl+b":
				m.viewing = 0
				return m, nil
			}
			if s := m.sessions[m.viewing]; s != nil && s.pane != nil {
				_, cmd := s.pane.Update(msg)
				return m, cmd
			}
			return m, nil
		}

		// Always-global keys (work even while a tab captures text).
		switch {
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case msg.String() == "ctrl+o":
			if m.lastSession != 0 && m.sessions[m.lastSession] != nil {
				m.viewing = m.lastSession
				if s := m.sessions[m.viewing]; s.pane != nil {
					s.pane.SetSize(m.width, m.paneHeight())
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.NextTab):
			m.active = (m.active + 1) % numTabs
			return m, nil
		case key.Matches(msg, m.keys.PrevTab):
			m.active = (m.active - 1 + numTabs) % numTabs
			return m, nil
		case key.Matches(msg, m.keys.CaptureIdea):
			mod := newIdeaModal(m.store, m.theme)
			mod.SetSize(m.width, m.height)
			m.modal = &mod
			return m, m.modal.Init()
		}

		// Letter/number shortcuts only when the active tab isn't editing text.
		if !m.tabs[m.active].CapturingInput() {
			switch {
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
	case paneDirtyMsg:
		if s := m.sessions[msg.id]; s != nil && s.pane != nil {
			_, cmd := s.pane.Update(msg)
			return m, cmd
		}
		return m, nil
	case paneExitedMsg:
		return (&m).handlePaneExited(msg)
	case launchPlanRequestMsg:
		return m, m.preparePlanCmd(msg)
	case planPreparedMsg:
		return (&m).startPlanSession(msg)
	case planPollMsg:
		return (&m).handlePlanPoll()
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

// View renders tab bar + active tab content (or modal) + status bar. When a
// session is attached (viewing != 0) it takes over the screen.
func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}

	if m.viewing != 0 {
		if s := m.sessions[m.viewing]; s != nil && s.pane != nil {
			exited := ""
			if s.exited {
				exited = m.theme.Subtle.Render(" (exited)")
			}
			bar := m.theme.StatusBar.Width(m.width).Render(
				fmt.Sprintf(" %s %s%s  %s",
					m.theme.Accent.Render("◆ session"),
					s.title, exited,
					m.theme.Help.Render("ctrl+b: detach · ctrl+c: quit")))
			return lipgloss.JoinVertical(lipgloss.Left, s.pane.View(), bar)
		}
		m.viewing = 0 // session vanished; fall back to tabs
	}

	tabBar := m.renderTabBar()

	var content string
	if m.modal != nil {
		content = m.modal.View()
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
	help := m.theme.Help.Render("tab: switch · ctrl+i: idea · q: quit · ?: help")
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

// liveSessionCount counts sessions whose pane has not exited.
func (m Model) liveSessionCount() int {
	n := 0
	for _, s := range m.sessions {
		if !s.exited {
			n++
		}
	}
	return n
}

// ctx is a convenience for tabs that need a context (short-lived ops).
func ctx() context.Context { return context.Background() }
