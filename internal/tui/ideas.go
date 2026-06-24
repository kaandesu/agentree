package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ideas renders the idea pile grouped by priority (1..5) — the "virtual
// folders" view over the ideas table. A cursor selects one idea; 1..5 overrides
// its priority inline, p promotes it into a Planner spec, e exports the pile.
type ideas struct {
	store  *store.Store
	theme  Theme
	w, h   int
	items  []store.Idea
	loaded bool
	cursor int
	list   list.Model
}

func newIdeas(st *store.Store, th Theme) *ideas {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(colActive).BorderForeground(colActive)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(colSubtle).BorderForeground(colActive)
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Ideas"
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	return &ideas{store: st, theme: th, list: l}
}

type ideaListItem struct{ idea store.Idea }

func (i ideaListItem) Title() string {
	return fmt.Sprintf("P%d  %s", i.idea.Priority, i.idea.Title)
}

func (i ideaListItem) Description() string {
	if strings.TrimSpace(i.idea.Rationale) != "" {
		return i.idea.Rationale
	}
	if strings.TrimSpace(i.idea.Body) != "" {
		return strings.TrimSpace(i.idea.Body)
	}
	return "captured idea"
}

func (i ideaListItem) FilterValue() string { return i.idea.Title }

type ideasLoadedMsg struct{ items []store.Idea }

// promoteIdeaMsg asks the root to open the Planner prefilled from this idea.
type promoteIdeaMsg struct{ idea store.Idea }

// deleteIdeaRequestMsg asks the root to confirm and hard-delete this idea.
type deleteIdeaRequestMsg struct{ idea store.Idea }

// statusMsg sets the root status line from a tab action (e.g. export path).
type statusMsg struct{ text string }

func (i *ideas) Init() tea.Cmd { return i.refresh() }

func (i *ideas) refresh() tea.Cmd {
	return func() tea.Msg {
		items, err := i.store.ListIdeas(ctx())
		if err != nil {
			return errMsg{err}
		}
		return ideasLoadedMsg{items}
	}
}

func (i *ideas) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case ideasLoadedMsg:
		i.items = msg.items
		i.loaded = true
		if i.cursor >= len(i.items) {
			i.cursor = len(i.items) - 1
		}
		if i.cursor < 0 {
			i.cursor = 0
		}
		i.refreshList()
	case ideaCapturedMsg:
		// A new idea was filed via the modal — refresh.
		return i, i.refresh()
	case tea.KeyMsg:
		return i.handleKey(msg)
	}
	return i, nil
}

func (i *ideas) handleKey(msg tea.KeyMsg) (tab, tea.Cmd) {
	if i.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		i.list, cmd = i.list.Update(msg)
		i.cursor = i.list.Index()
		return i, cmd
	}
	switch msg.String() {
	case "r":
		return i, i.refresh()
	case "up", "k":
		var cmd tea.Cmd
		i.list, cmd = i.list.Update(msg)
		i.cursor = i.list.Index()
		return i, cmd
	case "down", "j":
		var cmd tea.Cmd
		i.list, cmd = i.list.Update(msg)
		i.cursor = i.list.Index()
		return i, cmd
	case "1", "2", "3", "4", "5":
		if sel := i.selected(); sel != nil {
			p, _ := strconv.Atoi(msg.String())
			id := sel.ID
			return i, func() tea.Msg {
				if err := i.store.UpdateIdeaTriage(ctx(), id, p, "manual override"); err != nil {
					return errMsg{err}
				}
				items, err := i.store.ListIdeas(ctx())
				if err != nil {
					return errMsg{err}
				}
				return ideasLoadedMsg{items}
			}
		}
	case "p":
		if sel := i.selected(); sel != nil {
			idea := *sel
			return i, func() tea.Msg { return promoteIdeaMsg{idea: idea} }
		}
	case "x":
		if sel := i.selected(); sel != nil {
			idea := *sel
			return i, func() tea.Msg { return deleteIdeaRequestMsg{idea: idea} }
		}
	case "e":
		return i, i.exportCmd()
	}
	var cmd tea.Cmd
	i.list, cmd = i.list.Update(msg)
	i.cursor = i.list.Index()
	return i, cmd
}

// selected returns the idea under the cursor, or nil if the pile is empty.
func (i *ideas) selected() *store.Idea {
	item, ok := i.list.SelectedItem().(ideaListItem)
	if !ok {
		return nil
	}
	return &item.idea
}

// exportCmd writes the prioritized pile to a timestamped markdown file in the
// current working directory and reports the path on the status line.
func (i *ideas) exportCmd() tea.Cmd {
	items := i.items
	return func() tea.Msg {
		path := fmt.Sprintf("agentree-ideas-%s.md", time.Now().Format("20060102-150405"))
		if err := os.WriteFile(path, []byte(renderIdeasMarkdown(items)), 0o644); err != nil {
			return errMsg{err}
		}
		return statusMsg{text: "exported ideas → " + path}
	}
}

func renderIdeasMarkdown(items []store.Idea) string {
	var b strings.Builder
	b.WriteString("# Idea pile (by priority)\n\n")
	for pri := 1; pri <= 5; pri++ {
		var group []store.Idea
		for _, it := range items {
			if it.Priority == pri {
				group = append(group, it)
			}
		}
		if len(group) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("## P%d\n\n", pri))
		for _, it := range group {
			b.WriteString("- **" + it.Title + "**")
			if strings.TrimSpace(it.Rationale) != "" {
				b.WriteString(" — _" + it.Rationale + "_")
			}
			b.WriteString("\n")
			if strings.TrimSpace(it.Body) != "" {
				for _, line := range strings.Split(it.Body, "\n") {
					b.WriteString("  " + line + "\n")
				}
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (i *ideas) SetSize(w, h int) {
	i.w, i.h = w, h
	i.refreshList()
}

func (i *ideas) refreshList() {
	items := make([]list.Item, 0, len(i.items))
	for _, it := range i.items {
		items = append(items, ideaListItem{idea: it})
	}
	_ = i.list.SetItems(items)
	i.list.SetSize(fitDim(i.w-4), fitDim(i.h-5))
	i.list.Select(i.cursor)
}

func (i *ideas) View() string {
	var b strings.Builder
	if !i.loaded {
		b.WriteString(i.theme.Subtle.Render("loading…"))
	} else if len(i.items) == 0 {
		b.WriteString(i.theme.Subtle.Render("Empty. Press ctrl+n anywhere to pitch an idea — the brain triages it by priority."))
	} else {
		b.WriteString(i.list.View())
		b.WriteString(i.theme.Help.Render("↑/↓: move · 1–5: set priority · p: promote to plan · x: delete · e: export · r: refresh"))
	}
	return lipgloss.NewStyle().Width(i.w).Height(i.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab. The Ideas tab binds 1..5 to inline priority
// override, which would otherwise be swallowed by the global numeric tab-switch
// shortcuts, so it claims input while active. Tab navigation, ctrl+n and ctrl+c
// stay global; switch tabs from here with tab / shift+tab.
func (i *ideas) CapturingInput() bool { return true }
