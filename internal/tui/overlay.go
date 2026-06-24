package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
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
	keys  KeyMap
	help  help.Model
	w, h  int
}

func newHelpOverlay(th Theme, keys KeyMap) helpOverlay {
	hlp := help.New()
	hlp.ShowAll = true
	return helpOverlay{theme: th, keys: keys, help: hlp}
}

func (m *helpOverlay) SetSize(w, h int) { m.w, m.h = w, h }

func (m *helpOverlay) Update(msg tea.Msg) (bool, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		return true, nil
	}
	return false, nil
}

func (m *helpOverlay) View() string {
	t := m.theme
	m.help.Width = m.w - 8
	section := func(title string, lines ...string) string {
		return t.Accent.Render(title) + "\n  " + strings.Join(lines, "\n  ") + "\n"
	}
	body := t.Title.Render("agentree keys") + "\n\n" +
		m.help.View(m.keys) + "\n\n" +
		section("Dashboard",
			"j / k             select an agent",
			"d                 load its diff",
			"p / m / x         open PR · merge · discard",
			"r                 refresh tasks") + "\n" +
		section("Planner",
			"j / k             select project",
			"enter             compose spec",
			"ctrl+s / ctrl+f   plan first · split now",
			"esc               return to project list") + "\n" +
		section("Projects / Tasks",
			"j / k             move table selection",
			"a                 add project",
			"x                 remove task (Tasks)",
			"r                 refresh") + "\n" +
		section("Ideas",
			"j / k             move · 1–5 set priority",
			"p                 promote to Planner",
			"x                 delete idea",
			"e                 export pile to markdown") + "\n" +
		t.Help.Render("press any key to close")
	box := t.ModalBox.Render(body)
	return lipgloss.Place(m.w, m.h-2, lipgloss.Center, lipgloss.Center, box)
}
