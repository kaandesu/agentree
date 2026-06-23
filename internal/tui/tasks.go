package tui

import (
	"fmt"
	"strings"

	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// tasks shows the task queue with status. Lifecycle actions (launch, review,
// PR) arrive in later milestones.
type tasks struct {
	store  *store.Store
	theme  Theme
	w, h   int
	items  []store.Task
	loaded bool
}

func newTasks(st *store.Store, th Theme) *tasks {
	return &tasks{store: st, theme: th}
}

func (t *tasks) Init() tea.Cmd { return t.refresh() }

func (t *tasks) refresh() tea.Cmd {
	return func() tea.Msg {
		items, err := t.store.ListTasks(ctx())
		if err != nil {
			return errMsg{err}
		}
		return tasksLoadedMsg{items}
	}
}

func (t *tasks) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case tasksLoadedMsg:
		t.items = msg.tasks
		t.loaded = true
	case tea.KeyMsg:
		if msg.String() == "r" {
			return t, t.refresh()
		}
	}
	return t, nil
}

func (t *tasks) SetSize(w, h int) { t.w, t.h = w, h }

func (t *tasks) View() string {
	var b strings.Builder
	b.WriteString(t.theme.Title.Render("Task queue"))
	b.WriteString("\n\n")
	if !t.loaded {
		b.WriteString(t.theme.Subtle.Render("loading…"))
	} else if len(t.items) == 0 {
		b.WriteString(t.theme.Subtle.Render("No tasks yet."))
	} else {
		for _, it := range t.items {
			b.WriteString(fmt.Sprintf("%s  [%s]  %s\n", statusDot(it.Status), it.AgentKind, it.Title))
		}
	}
	return lipgloss.NewStyle().Width(t.w).Height(t.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab; the tasks list never captures text.
func (t *tasks) CapturingInput() bool { return false }
