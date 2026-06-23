package tui

import (
	"fmt"
	"strings"

	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type plannerPhase int

const (
	phaseSelectProject plannerPhase = iota
	phaseCompose
)

// planner is the spec composer. First pick a registered project, then pour a
// long spec into the textarea. On submit it emits a launchPlanRequestMsg; the
// root expands the spec via the brain and launches claude plan mode.
type planner struct {
	store *store.Store
	theme Theme
	w, h  int

	phase    plannerPhase
	projects []store.Project
	cursor   int
	selected *store.Project
	ta       textarea.Model
	notice   string
}

// launchPlanRequestMsg asks the root to start a planning session.
type launchPlanRequestMsg struct {
	project store.Project
	spec    string
}

func newPlanner(st *store.Store, th Theme) *planner {
	ta := textarea.New()
	ta.Placeholder = "Describe the feature/product in as much detail as you want. The more you write, the harder the planner interrogates you…"
	ta.CharLimit = 0
	return &planner{store: st, theme: th, phase: phaseSelectProject, ta: ta}
}

func (p *planner) Init() tea.Cmd { return p.refresh() }

// selectProject jumps straight to the compose phase for a given project (used
// when agentree is launched with a repo path argument).
func (p *planner) selectProject(pr store.Project) {
	p.selected = &pr
	p.phase = phaseCompose
	p.ta.Focus()
}

func (p *planner) refresh() tea.Cmd {
	return func() tea.Msg {
		items, err := p.store.ListProjects(ctx())
		if err != nil {
			return errMsg{err}
		}
		return projectsLoadedMsg{items}
	}
}

func (p *planner) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsLoadedMsg:
		p.projects = msg.items
		if p.cursor >= len(p.projects) {
			p.cursor = 0
		}
		return p, nil
	case planSessionStartedMsg:
		// Root accepted our request; reset compose state.
		p.notice = "launched plan session for: " + msg.title
		p.ta.Reset()
		p.phase = phaseSelectProject
		p.selected = nil
		return p, nil
	case tea.KeyMsg:
		if p.phase == phaseSelectProject {
			return p.updateSelect(msg)
		}
		return p.updateCompose(msg)
	}
	return p, nil
}

func (p *planner) updateSelect(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.projects)-1 {
			p.cursor++
		}
	case "r":
		return p, p.refresh()
	case "enter":
		if len(p.projects) == 0 {
			p.notice = "no projects — register one in the Projects tab (press 3, then a)"
			return p, nil
		}
		sel := p.projects[p.cursor]
		p.selected = &sel
		p.phase = phaseCompose
		p.notice = ""
		p.ta.Focus()
		return p, textarea.Blink
	}
	return p, nil
}

func (p *planner) updateCompose(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.phase = phaseSelectProject
		p.ta.Blur()
		return p, nil
	case "ctrl+s":
		spec := strings.TrimSpace(p.ta.Value())
		if spec == "" || p.selected == nil {
			return p, nil
		}
		return p, func() tea.Msg {
			return launchPlanRequestMsg{project: *p.selected, spec: spec}
		}
	}
	var cmd tea.Cmd
	p.ta, cmd = p.ta.Update(msg)
	return p, cmd
}

func (p *planner) SetSize(w, h int) {
	p.w, p.h = w, h
	p.ta.SetWidth(w - 4)
	th := h - 7
	if th < 3 {
		th = 3
	}
	p.ta.SetHeight(th)
}

func (p *planner) View() string {
	var b strings.Builder
	b.WriteString(p.theme.Title.Render("Plan a feature"))
	b.WriteString("\n\n")

	if p.phase == phaseSelectProject {
		b.WriteString(p.theme.Subtle.Render("Select a project:"))
		b.WriteString("\n\n")
		if len(p.projects) == 0 {
			b.WriteString(p.theme.Subtle.Render("(none registered — go to Projects tab)"))
		}
		for i, pr := range p.projects {
			cursor := "  "
			line := pr.Name + "  " + p.theme.Subtle.Render(pr.RepoPath)
			if i == p.cursor {
				cursor = p.theme.Accent.Render("▸ ")
				line = p.theme.Accent.Render(pr.Name) + "  " + p.theme.Subtle.Render(pr.RepoPath)
			}
			b.WriteString(cursor + line + "\n")
		}
		b.WriteString("\n")
		if p.notice != "" {
			b.WriteString(p.theme.Accent.Render(p.notice) + "\n")
		}
		b.WriteString(p.theme.Help.Render("↑/↓: move · enter: select · r: refresh"))
	} else {
		b.WriteString(p.theme.Subtle.Render(fmt.Sprintf("Project: %s", p.selected.Name)))
		b.WriteString("\n")
		b.WriteString(p.ta.View())
		b.WriteString("\n")
		b.WriteString(p.theme.Help.Render("ctrl+s: expand & launch plan mode · esc: back"))
	}
	return lipgloss.NewStyle().Width(p.w).Height(p.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab; the planner always handles its own keys
// (project selection digits/nav and textarea editing).
func (p *planner) CapturingInput() bool { return true }
