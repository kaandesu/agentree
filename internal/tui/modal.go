package tui

import (
	"strconv"
	"strings"

	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ideaModal is the global idea-capture overlay (opened with ctrl+i). The user
// types an idea; on save it's filed. For M0 priority defaults to 3; the brain's
// auto-triage (M4) will assign 1..5 and may suggest promotion to the queue.
type ideaModal struct {
	store *store.Store
	theme Theme
	w, h  int
	ta    textarea.Model
}

// ideaCapturedMsg is emitted after an idea is filed so tabs can refresh.
type ideaCapturedMsg struct{ id int64 }

func newIdeaModal(st *store.Store, th Theme) ideaModal {
	ta := textarea.New()
	ta.Placeholder = "Pitch your idea… first line becomes the title."
	ta.Focus()
	return ideaModal{store: st, theme: th, ta: ta}
}

func (m *ideaModal) Init() tea.Cmd { return textarea.Blink }

func (m *ideaModal) SetSize(w, h int) {
	m.w, m.h = w, h
	bw := min(w-8, 80)
	if bw < 20 {
		bw = 20
	}
	m.ta.SetWidth(bw - 4)
	m.ta.SetHeight(6)
}

// Update returns done=true when the modal should close.
func (m *ideaModal) Update(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return true, nil
		case "ctrl+s":
			text := strings.TrimSpace(m.ta.Value())
			if text == "" {
				return true, nil
			}
			return true, m.save(text)
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return false, cmd
}

func (m *ideaModal) save(text string) tea.Cmd {
	return func() tea.Msg {
		title, body := splitTitleBody(text)
		idea, err := m.store.CreateIdea(ctx(), store.Idea{
			Title:    title,
			Body:     body,
			Priority: 3, // brain triage (M4) will refine
			Source:   "user",
		})
		if err != nil {
			return errMsg{err}
		}
		_ = m.store.AppendEvent(ctx(), "idea.added", `{"id":`+strconv.FormatInt(idea.ID, 10)+`}`)
		return ideaCapturedMsg{id: idea.ID}
	}
}

func (m *ideaModal) View() string {
	box := m.theme.ModalBox.Render(
		m.theme.Title.Render("Capture idea") + "\n\n" +
			m.ta.View() + "\n\n" +
			m.theme.Help.Render("ctrl+s: file  ·  esc: cancel"),
	)
	return lipgloss.Place(m.w, m.h-2, lipgloss.Center, lipgloss.Center, box)
}

func splitTitleBody(text string) (title, body string) {
	parts := strings.SplitN(text, "\n", 2)
	title = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		body = strings.TrimSpace(parts[1])
	}
	if len(title) > 120 {
		title = title[:120]
	}
	return title, body
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
