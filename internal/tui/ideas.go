package tui

import (
	"fmt"
	"strings"

	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ideas renders the idea pile grouped by priority (1..5) — the "virtual
// folders" view over the ideas table.
type ideas struct {
	store  *store.Store
	theme  Theme
	w, h   int
	items  []store.Idea
	loaded bool
}

func newIdeas(st *store.Store, th Theme) *ideas {
	return &ideas{store: st, theme: th}
}

type ideasLoadedMsg struct{ items []store.Idea }

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
	case ideaCapturedMsg:
		// A new idea was filed via the modal — refresh.
		return i, i.refresh()
	case tea.KeyMsg:
		if msg.String() == "r" {
			return i, i.refresh()
		}
	}
	return i, nil
}

func (i *ideas) SetSize(w, h int) { i.w, i.h = w, h }

func (i *ideas) View() string {
	var b strings.Builder
	b.WriteString(i.theme.Title.Render("Idea pile (by priority)"))
	b.WriteString("\n\n")
	if !i.loaded {
		b.WriteString(i.theme.Subtle.Render("loading…"))
	} else if len(i.items) == 0 {
		b.WriteString(i.theme.Subtle.Render("Empty. Press ctrl+i anywhere to pitch an idea — the brain auto-files it by priority."))
	} else {
		byPri := map[int][]store.Idea{}
		for _, it := range i.items {
			byPri[it.Priority] = append(byPri[it.Priority], it)
		}
		for pri := 1; pri <= 5; pri++ {
			group := byPri[pri]
			if len(group) == 0 {
				continue
			}
			b.WriteString(i.theme.Accent.Render(fmt.Sprintf("P%d", pri)))
			b.WriteString("\n")
			for _, it := range group {
				b.WriteString("  • " + it.Title + "\n")
			}
		}
	}
	return lipgloss.NewStyle().Width(i.w).Height(i.h).Padding(1, 2).Render(b.String())
}

// CapturingInput implements tab; the ideas list never captures text.
func (i *ideas) CapturingInput() bool { return false }
