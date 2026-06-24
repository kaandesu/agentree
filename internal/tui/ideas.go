package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"agentree/internal/store"

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
}

func newIdeas(st *store.Store, th Theme) *ideas {
	return &ideas{store: st, theme: th}
}

type ideasLoadedMsg struct{ items []store.Idea }

// promoteIdeaMsg asks the root to open the Planner prefilled from this idea.
type promoteIdeaMsg struct{ idea store.Idea }

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
	case ideaCapturedMsg:
		// A new idea was filed via the modal — refresh.
		return i, i.refresh()
	case tea.KeyMsg:
		return i.handleKey(msg)
	}
	return i, nil
}

func (i *ideas) handleKey(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "r":
		return i, i.refresh()
	case "up", "k":
		if i.cursor > 0 {
			i.cursor--
		}
	case "down", "j":
		if i.cursor < len(i.items)-1 {
			i.cursor++
		}
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
	case "e":
		return i, i.exportCmd()
	}
	return i, nil
}

// selected returns the idea under the cursor, or nil if the pile is empty.
func (i *ideas) selected() *store.Idea {
	if i.cursor < 0 || i.cursor >= len(i.items) {
		return nil
	}
	return &i.items[i.cursor]
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

func (i *ideas) SetSize(w, h int) { i.w, i.h = w, h }

func (i *ideas) View() string {
	var b strings.Builder
	b.WriteString(i.theme.Title.Render("Idea pile (by priority)"))
	b.WriteString("\n\n")
	if !i.loaded {
		b.WriteString(i.theme.Subtle.Render("loading…"))
	} else if len(i.items) == 0 {
		b.WriteString(i.theme.Subtle.Render("Empty. Press ctrl+n anywhere to pitch an idea — the brain triages it by priority."))
	} else {
		lastPri := 0
		for idx, it := range i.items {
			if it.Priority != lastPri {
				if lastPri != 0 {
					b.WriteString("\n")
				}
				b.WriteString(i.theme.Accent.Render(fmt.Sprintf("P%d", it.Priority)) + "\n")
				lastPri = it.Priority
			}
			cursor := "  "
			title := it.Title
			if idx == i.cursor {
				cursor = i.theme.Accent.Render("▸ ")
				title = i.theme.Accent.Render(it.Title)
			}
			b.WriteString(cursor + "• " + title + "\n")
			if idx == i.cursor && strings.TrimSpace(it.Rationale) != "" {
				b.WriteString("    " + i.theme.Subtle.Render(it.Rationale) + "\n")
			}
		}
		b.WriteString("\n")
		b.WriteString(i.theme.Help.Render("↑/↓: move · 1–5: set priority · p: promote to plan · e: export · r: refresh"))
	}
	return lipgloss.NewStyle().Width(i.w).Height(i.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab. The Ideas tab binds 1..5 to inline priority
// override, which would otherwise be swallowed by the global numeric tab-switch
// shortcuts, so it claims input while active. Tab navigation, ctrl+n and ctrl+c
// stay global; switch tabs from here with tab / shift+tab.
func (i *ideas) CapturingInput() bool { return true }
