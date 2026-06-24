package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// overlay is a modal layer that covers the active tab and owns all input until
// it reports done=true from Update. The idea-capture modal, confirm prompts,
// and the help overlay all implement it.
type overlay interface {
	Update(tea.Msg) (done bool, cmd tea.Cmd)
	View() string
	SetSize(w, h int)
}

// confirmModal asks the user to confirm a hard-to-reverse action (PR / merge /
// discard). On yes it returns the carried command; on no/esc it just closes.
type confirmModal struct {
	theme  Theme
	w, h   int
	prompt string
	detail string
	onYes  tea.Cmd
}

func newConfirmModal(th Theme, prompt, detail string, onYes tea.Cmd) confirmModal {
	return confirmModal{theme: th, prompt: prompt, detail: detail, onYes: onYes}
}

func (m *confirmModal) SetSize(w, h int) { m.w, m.h = w, h }

func (m *confirmModal) Update(msg tea.Msg) (bool, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "y", "enter":
			return true, m.onYes
		case "n", "esc":
			return true, nil
		}
	}
	return false, nil
}

func (m *confirmModal) View() string {
	body := m.theme.Title.Render(m.prompt) + "\n\n"
	if m.detail != "" {
		body += m.theme.Subtle.Render(m.detail) + "\n\n"
	}
	body += m.theme.Help.Render("y/enter: confirm · n/esc: cancel")
	box := m.theme.ModalBox.Render(body)
	return lipgloss.Place(m.w, m.h-2, lipgloss.Center, lipgloss.Center, box)
}

// helpOverlay lists global and per-tab keybindings. Any key dismisses it.
type helpOverlay struct {
	theme Theme
	w, h  int
}

func newHelpOverlay(th Theme) helpOverlay { return helpOverlay{theme: th} }

func (m *helpOverlay) SetSize(w, h int) { m.w, m.h = w, h }

func (m *helpOverlay) Update(msg tea.Msg) (bool, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return true, nil
	}
	return false, nil
}

func (m *helpOverlay) View() string {
	t := m.theme
	section := func(title string, lines ...string) string {
		s := t.Accent.Render(title) + "\n"
		for _, l := range lines {
			s += "  " + l + "\n"
		}
		return s
	}
	body := t.Title.Render("agentree — keys") + "\n\n" +
		section("Global",
			"tab / shift+tab   switch tabs",
			"1–5               jump to tab (when not editing)",
			"ctrl+n            capture an idea",
			"ctrl+o            attach to tmux (live agents)",
			"?                 this help",
			"q / ctrl+c        quit") + "\n" +
		section("Dashboard",
			"j / k             select an agent",
			"d                 load its diff",
			"p / m / x         open PR · merge · discard",
			"r                 refresh tasks") + "\n" +
		section("Ideas",
			"j / k             move · 1–5 set priority",
			"p                 promote to Planner",
			"e                 export pile to markdown") + "\n" +
		t.Help.Render("press any key to close")
	box := t.ModalBox.Render(body)
	return lipgloss.Place(m.w, m.h-2, lipgloss.Center, lipgloss.Center, box)
}
