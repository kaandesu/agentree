package tui

import (
	"path/filepath"
	"strings"

	"agentree/internal/orchestrator"
	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// projects lists registered repositories and supports registering new ones by
// path (press `a`). Registration validates the path is a git work tree.
type projects struct {
	store  *store.Store
	theme  Theme
	w, h   int
	items  []store.Project
	loaded bool

	adding bool
	input  textinput.Model
	table  table.Model
	errMsg string
}

func newProjects(st *store.Store, th Theme) *projects {
	ti := textinput.New()
	ti.Placeholder = "/absolute/path/to/repo"
	ti.Prompt = "repo path › "
	tbl := table.New(table.WithFocused(true))
	return &projects{store: st, theme: th, input: ti, table: tbl}
}

type projectsLoadedMsg struct{ items []store.Project }
type projectRegisteredMsg struct{ name string }

func (p *projects) Init() tea.Cmd { return p.refresh() }

func (p *projects) refresh() tea.Cmd {
	return func() tea.Msg {
		items, err := p.store.ListProjects(ctx())
		if err != nil {
			return errMsg{err}
		}
		return projectsLoadedMsg{items}
	}
}

// register validates the path and inserts the project (idempotent).
func (p *projects) register(path string) tea.Cmd {
	return func() tea.Msg {
		abs, err := filepath.Abs(path)
		if err != nil {
			return errMsg{err}
		}
		name, branch, err := orchestrator.RepoInfo(ctx(), abs)
		if err != nil {
			return errMsg{err}
		}
		if _, err := p.store.GetOrCreateProject(ctx(), name, abs, branch); err != nil {
			return errMsg{err}
		}
		return projectRegisteredMsg{name: name}
	}
}

func (p *projects) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsLoadedMsg:
		p.items = msg.items
		p.loaded = true
		p.refreshTable()
		return p, nil
	case projectRegisteredMsg:
		p.adding = false
		p.errMsg = ""
		p.input.Reset()
		p.input.Blur()
		return p, p.refresh()
	case errMsg:
		if p.adding {
			p.errMsg = msg.err.Error()
		}
		return p, nil
	case tea.KeyMsg:
		if p.adding {
			switch msg.String() {
			case "esc":
				p.adding = false
				p.errMsg = ""
				p.input.Blur()
				return p, nil
			case "enter":
				path := strings.TrimSpace(p.input.Value())
				if path == "" {
					return p, nil
				}
				return p, p.register(path)
			}
			var cmd tea.Cmd
			p.input, cmd = p.input.Update(msg)
			return p, cmd
		}
		switch msg.String() {
		case "a":
			p.adding = true
			p.errMsg = ""
			p.input.Focus()
			return p, textinput.Blink
		case "r":
			return p, p.refresh()
		}
		var cmd tea.Cmd
		p.table, cmd = p.table.Update(msg)
		return p, cmd
	}
	return p, nil
}

func (p *projects) SetSize(w, h int) {
	p.w, p.h = w, h
	p.refreshTable()
}

func (p *projects) refreshTable() {
	nameW := 18
	branchW := 12
	pathW := p.w - nameW - branchW - 8
	if pathW < 18 {
		pathW = 18
	}
	p.table.SetColumns([]table.Column{
		{Title: "Project", Width: nameW},
		{Title: "Branch", Width: branchW},
		{Title: "Path", Width: pathW},
	})
	rows := make([]table.Row, 0, len(p.items))
	for _, it := range p.items {
		rows = append(rows, table.Row{it.Name, it.DefaultBranch, it.RepoPath})
	}
	p.table.SetRows(rows)
	p.table.SetWidth(fitDim(p.w - 4))
	p.table.SetHeight(fitDim(p.h - 6))
	p.table.Focus()
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Foreground(colActive).Bold(true)
	styles.Selected = styles.Selected.Foreground(colFgLight).Background(colBorder)
	p.table.SetStyles(styles)
}

func (p *projects) View() string {
	var b strings.Builder

	if p.adding {
		b.WriteString(p.theme.Title.Render("Add project"))
		b.WriteString("\n\n")
		b.WriteString(p.input.View())
		b.WriteString("\n")
		if p.errMsg != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("✗ " + p.errMsg))
			b.WriteString("\n")
		}
		b.WriteString("\n" + p.theme.Help.Render("enter: register · esc: cancel"))
		return lipgloss.NewStyle().Width(p.w).Height(p.h).Padding(1, 2).Render(b.String())
	}

	if !p.loaded {
		b.WriteString(p.theme.Subtle.Render("loading…"))
	} else if len(p.items) == 0 {
		b.WriteString(p.theme.Subtle.Render("No projects registered yet."))
	} else {
		b.WriteString(p.table.View())
	}
	b.WriteString("\n" + p.theme.Help.Render("a: add project · r: refresh"))
	return lipgloss.NewStyle().Width(p.w).Height(p.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab; true only while entering a repo path.
func (p *projects) CapturingInput() bool { return p.adding }
