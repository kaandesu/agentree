package tui

import (
	"fmt"
	"strings"

	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashboard shows live agents (tmux windows) and the task backlog. Agents run
// in a dedicated tmux server; the user presses ctrl+o to attach and interact.
type dashboard struct {
	store   *store.Store
	theme   Theme
	w, h    int
	tasks   []store.Task
	loaded  bool
	windows []windowView
	attach  string
}

func newDashboard(st *store.Store, th Theme) *dashboard {
	return &dashboard{store: st, theme: th}
}

// tasksLoadedMsg carries a refreshed task list.
type tasksLoadedMsg struct{ tasks []store.Task }

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
	case tea.KeyMsg:
		if msg.String() == "r" {
			return d, d.refresh()
		}
	}
	return d, nil
}

func (d *dashboard) SetSize(w, h int) { d.w, d.h = w, h }

func (d *dashboard) View() string {
	var b strings.Builder
	b.WriteString(d.theme.Title.Render("Live agents"))
	b.WriteString("\n\n")

	if len(d.windows) == 0 {
		b.WriteString(d.theme.Subtle.Render("No agents running. Press 2 to open the Planner and launch one."))
	} else {
		for _, w := range d.windows {
			b.WriteString(fmt.Sprintf("%s  %-6s  %s\n", agentDot(w), w.kind, w.title))
		}
		b.WriteString("\n")
		b.WriteString(d.theme.Help.Render("ctrl+o: attach (tmux — ctrl+b n/p to cycle agents, ctrl+b d to detach)"))
		if d.attach != "" {
			b.WriteString("\n")
			b.WriteString(d.theme.Subtle.Render("or from another terminal: " + d.attach))
		}
	}

	b.WriteString("\n\n")
	b.WriteString(d.theme.Title.Render("Tasks"))
	b.WriteString("\n\n")
	if !d.loaded {
		b.WriteString(d.theme.Subtle.Render("loading…"))
	} else if len(d.tasks) == 0 {
		b.WriteString(d.theme.Subtle.Render("No tasks yet. Press 2 to open the Planner, or ctrl+i to capture an idea."))
	} else {
		for _, t := range d.tasks {
			dot := statusDot(t.Status)
			line := fmt.Sprintf("%s  %-10s  %s", dot, t.Status, t.Title)
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(d.theme.Help.Render("r: refresh"))
	return lipgloss.NewStyle().Width(d.w).Height(d.h).Padding(1, 2).Render(b.String())
}

// agentDot colors an agent's status: green=running, grey=exited cleanly,
// red=exited with error.
func agentDot(w windowView) string {
	if !w.dead {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("●")
	}
	if w.status != 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("●")
	}
	return lipgloss.NewStyle().Foreground(colSubtle).Render("●")
}

func statusDot(s store.TaskStatus) string {
	switch s {
	case store.StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("●")
	case store.StatusReady, store.StatusReview:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("●")
	case store.StatusDone:
		return lipgloss.NewStyle().Foreground(colSubtle).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(colSubtle).Render("○")
	}
}

// errMsg is a generic error carrier used by tab commands.
type errMsg struct{ err error }

// CapturingInput implements tab; the dashboard never captures text.
func (d *dashboard) CapturingInput() bool { return false }
