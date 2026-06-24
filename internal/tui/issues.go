package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// issues is a read-only list of the active project's open GitHub issues, fetched
// via the `gh` CLI (no GitHub API token handling of our own). Press r to
// refresh; selection is via the table's own j/k.
type issues struct {
	theme   Theme
	w, h    int
	project *store.Project
	table   table.Model
	items   []ghIssue
	loaded  bool
	loading bool
	errMsg  string
}

// ghIssue mirrors the JSON shape of `gh issue list --json ...`.
type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type issuesLoadedMsg struct {
	items []ghIssue
	err   error
}

func newIssues(th Theme) *issues {
	tbl := table.New(table.WithFocused(true))
	return &issues{theme: th, table: tbl}
}

func (i *issues) Init() tea.Cmd { return nil }

// setProject points the tab at a repo and kicks off a fetch.
func (i *issues) setProject(p store.Project) tea.Cmd {
	pp := p
	i.project = &pp
	i.loaded = false
	i.errMsg = ""
	return i.refresh()
}

// refresh fetches issues off the Update loop and emits issuesLoadedMsg.
func (i *issues) refresh() tea.Cmd {
	if i.project == nil {
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return func() tea.Msg {
			return issuesLoadedMsg{err: fmt.Errorf("gh CLI not found on PATH — install GitHub CLI to list issues")}
		}
	}
	i.loading = true
	repo := i.project.RepoPath
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, "gh", "issue", "list",
			"--json", "number,title,labels,author", "--limit", "50")
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			stderr := ""
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			if stderr == "" {
				stderr = err.Error()
			}
			return issuesLoadedMsg{err: fmt.Errorf("%s", stderr)}
		}
		var items []ghIssue
		if err := json.Unmarshal(out, &items); err != nil {
			return issuesLoadedMsg{err: err}
		}
		return issuesLoadedMsg{items: items}
	}
}

func (i *issues) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case issuesLoadedMsg:
		i.loading = false
		i.loaded = true
		if msg.err != nil {
			i.errMsg = msg.err.Error()
			i.items = nil
		} else {
			i.errMsg = ""
			i.items = msg.items
		}
		i.refreshTable()
		return i, nil
	case tea.KeyMsg:
		if msg.String() == "r" {
			return i, i.refresh()
		}
		var cmd tea.Cmd
		i.table, cmd = i.table.Update(msg)
		return i, cmd
	}
	return i, nil
}

func (i *issues) SetSize(w, h int) {
	i.w, i.h = w, h
	i.refreshTable()
}

func (i *issues) refreshTable() {
	numW := 6
	authW := 14
	labelW := 20
	titleW := i.w - numW - authW - labelW - 10
	if titleW < 20 {
		titleW = 20
	}
	i.table.SetColumns([]table.Column{
		{Title: "#", Width: numW},
		{Title: "Title", Width: titleW},
		{Title: "Labels", Width: labelW},
		{Title: "Author", Width: authW},
	})
	rows := make([]table.Row, 0, len(i.items))
	for _, it := range i.items {
		labels := make([]string, 0, len(it.Labels))
		for _, l := range it.Labels {
			labels = append(labels, l.Name)
		}
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", it.Number),
			it.Title,
			strings.Join(labels, ", "),
			it.Author.Login,
		})
	}
	i.table.SetRows(rows)
	i.table.SetWidth(fitDim(i.w - 4))
	i.table.SetHeight(fitDim(i.h - 4))
	i.table.Focus()
	styles := table.DefaultStyles()
	styles.Header = styles.Header.Foreground(colActive).Bold(true)
	styles.Selected = styles.Selected.Foreground(colFgLight).Background(colBorder)
	i.table.SetStyles(styles)
}

func (i *issues) View() string {
	var b strings.Builder
	switch {
	case i.project == nil:
		b.WriteString(i.theme.Subtle.Render("No active project. Pick one in Projects (enter) or the Planner."))
	case i.loading && !i.loaded:
		b.WriteString(i.theme.Subtle.Render("loading issues for " + i.project.Name + "…"))
	case i.errMsg != "":
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("✗ " + i.errMsg))
	case len(i.items) == 0 && i.loaded:
		b.WriteString(i.theme.Subtle.Render("No open issues for " + i.project.Name + "."))
	default:
		b.WriteString(i.theme.Subtle.Render(fmt.Sprintf("Issues · %s", i.project.Name)))
		b.WriteString("\n\n")
		b.WriteString(i.table.View())
	}
	b.WriteString("\n" + i.theme.Help.Render("r: refresh · j/k: move"))
	return lipgloss.NewStyle().Width(i.w).Height(i.h).Padding(1, 2).Render(b.String())
}

// CapturingInput is false: the tab only uses r and table navigation, leaving
// digit tab-switching and q-quit global.
func (i *issues) CapturingInput() bool { return false }
