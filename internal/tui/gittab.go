package tui

import (
	"os"
	"os/exec"

	"agentree/internal/orchestrator"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// gitPaneID tags the lazygit pane's redraw/exit messages. Agents run as tmux
// windows (not paneModels), so this is the only live paneModel in the TUI and
// the constant can't collide.
const gitPaneID = 9001

// gittab hosts lazygit live inside a tab via the PTY pane infrastructure
// (newPaneModel + orchestrator.StartPane). It targets the active project's repo
// and lazily (re)starts lazygit when a project + size are available. lazygit
// persists in the background while other tabs are shown.
type gittab struct {
	theme   Theme
	w, h    int
	project *store.Project
	pane    *paneModel
	exited  bool
	missing bool
	err     error
}

func newGitTab(th Theme) *gittab { return &gittab{theme: th} }

func (g *gittab) Init() tea.Cmd { return nil }

// setProject points the tab at a repo, tearing down any pane for the previous
// one. The pane relaunches lazily (next Update) once a size is known.
func (g *gittab) setProject(p store.Project) tea.Cmd {
	pp := p
	g.project = &pp
	if g.pane != nil {
		_ = g.pane.Close()
		g.pane = nil
	}
	g.exited = false
	g.err = nil
	return g.ensurePane()
}

// ensurePane starts lazygit once both a project and a size are available. It is
// a no-op if a pane is already running or prerequisites are missing.
func (g *gittab) ensurePane() tea.Cmd {
	if g.pane != nil || g.project == nil || g.w <= 0 || g.h <= 0 {
		return nil
	}
	if _, err := exec.LookPath("lazygit"); err != nil {
		g.missing = true
		return nil
	}
	g.missing = false
	repo := g.project.RepoPath
	start := func(onUpdate func(), cols, rows int) (*orchestrator.Pane, error) {
		c := exec.Command("lazygit", "-p", repo)
		c.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
		return orchestrator.StartPane(c, cols, rows, onUpdate)
	}
	pane, err := newPaneModel(gitPaneID, start, g.w, g.h)
	if err != nil {
		g.err = err
		return nil
	}
	pane.focused = true
	g.pane = pane
	g.exited = false
	return g.pane.Init()
}

func (g *gittab) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case paneExitedMsg:
		if g.pane != nil && msg.id == gitPaneID {
			_ = g.pane.Close()
			g.pane = nil
			g.exited = true
			g.err = msg.err
		}
		return g, nil
	case paneDirtyMsg:
		if g.pane != nil {
			_, cmd := g.pane.Update(msg)
			return g, cmd
		}
		return g, nil
	case tea.KeyMsg:
		if g.pane != nil {
			_, cmd := g.pane.Update(msg)
			return g, cmd
		}
		if msg.String() == "r" {
			return g, g.ensurePane()
		}
	}
	// Lazy start: covers the case where the project was set before the first
	// resize, so neither setProject nor SetSize could start the pane.
	if g.pane == nil {
		if cmd := g.ensurePane(); cmd != nil {
			return g, cmd
		}
	}
	return g, nil
}

func (g *gittab) SetSize(w, h int) {
	g.w, g.h = w, h
	if g.pane != nil {
		g.pane.SetSize(w, h)
	}
}

func (g *gittab) View() string {
	switch {
	case g.missing:
		return g.theme.Subtle.Render("lazygit not found on PATH — install it (github.com/jesseduffield/lazygit), then press r")
	case g.project == nil:
		return g.theme.Subtle.Render("No active project. Pick one in Projects (enter) or the Planner, then it shows here.")
	case g.err != nil:
		return g.theme.Subtle.Render("lazygit error: " + g.err.Error() + "  (press r to retry)")
	case g.pane == nil && g.exited:
		return g.theme.Subtle.Render("lazygit closed — press r to relaunch")
	case g.pane == nil:
		return g.theme.Subtle.Render("starting lazygit…")
	default:
		return g.pane.View()
	}
}

// CapturingInput keeps digits/letters/q flowing to lazygit while it's running.
// The always-global keys (tab/shift+tab, ctrl+c, ctrl+o, ctrl+n) stay reserved
// by the root, so use tab or a number key to leave lazygit.
func (g *gittab) CapturingInput() bool { return g.pane != nil }
