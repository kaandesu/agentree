package tui

import (
	"strings"

	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/table"
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
	table  table.Model
}

func newTasks(st *store.Store, th Theme) *tasks {
	tbl := table.New(table.WithFocused(true))
	return &tasks{store: st, theme: th, table: tbl}
}

// removeTaskRequestMsg asks the root to confirm removing this task — killing any
// live agents (worktrees kept) and marking it discarded.
type removeTaskRequestMsg struct{ task store.Task }

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
		t.refreshTable()
	case tea.KeyMsg:
		return t.handleKey(msg)
	}
	return t, nil
}

func (t *tasks) handleKey(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "r":
		return t, t.refresh()
	case "x":
		if t.loaded && len(t.items) > 0 {
			task := t.items[t.table.Cursor()]
			return t, func() tea.Msg { return removeTaskRequestMsg{task: task} }
		}
		return t, nil
	}
	var cmd tea.Cmd
	t.table, cmd = t.table.Update(msg)
	return t, cmd
}

func (t *tasks) SetSize(w, h int) {
	t.w, t.h = w, h
	t.refreshTable()
}

func (t *tasks) refreshTable() {
	statusW := 12
	agentW := 10
	titleW := t.w - statusW - agentW - 8
	if titleW < 20 {
		titleW = 20
	}
	t.table.SetColumns([]table.Column{
		{Title: "Status", Width: statusW},
		{Title: "Agent", Width: agentW},
		{Title: "Task", Width: titleW},
	})
	rows := make([]table.Row, 0, len(t.items))
	for _, it := range t.items {
		rows = append(rows, table.Row{string(it.Status), string(it.AgentKind), it.Title})
	}
	t.table.SetRows(rows)
	t.table.SetWidth(fitDim(t.w - 4))
	t.table.SetHeight(fitDim(t.h - 5))
	t.table.Focus()
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Foreground(colActive).Bold(true)
	styles.Selected = styles.Selected.Foreground(colFgLight).Background(colBorder)
	t.table.SetStyles(styles)
}

func (t *tasks) View() string {
	var b strings.Builder
	if !t.loaded {
		b.WriteString(t.theme.Subtle.Render("loading…"))
	} else if len(t.items) == 0 {
		b.WriteString(t.theme.Subtle.Render("No tasks yet."))
	} else {
		b.WriteString(t.table.View())
	}
	b.WriteString("\n" + t.theme.Help.Render("j/k: move · x: remove · r: refresh"))
	return lipgloss.NewStyle().Width(t.w).Height(t.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab; the tasks list never captures text.
func (t *tasks) CapturingInput() bool { return false }
