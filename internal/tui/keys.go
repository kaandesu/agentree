package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds the global keybindings. Tab-local bindings live in their models.
type KeyMap struct {
	NextTab     key.Binding
	PrevTab     key.Binding
	Tab1        key.Binding
	Tab2        key.Binding
	Tab3        key.Binding
	Tab4        key.Binding
	Tab5        key.Binding
	Tab6        key.Binding
	Tab7        key.Binding
	CaptureIdea key.Binding
	Attach      key.Binding
	Quit        key.Binding
	Help        key.Binding
}

// ShortHelp implements help.KeyMap for the footer.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextTab, k.CaptureIdea, k.Attach, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap for expanded global help.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.NextTab, k.PrevTab, k.Tab1, k.Tab2, k.Tab3},
		{k.Tab4, k.Tab5, k.CaptureIdea, k.Attach, k.Help, k.Quit},
	}
}

// DefaultKeyMap returns the standard global bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		NextTab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next"),
		),
		PrevTab: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev"),
		),
		Tab1: key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "dashboard")),
		Tab2: key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "planner")),
		Tab3: key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "projects")),
		Tab4: key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "ideas")),
		Tab5: key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "tasks")),
		Tab6: key.NewBinding(key.WithKeys("6"), key.WithHelp("6", "git")),
		Tab7: key.NewBinding(key.WithKeys("7"), key.WithHelp("7", "issues")),
		// ctrl+i is byte 0x09 — identical to Tab in every standard terminal, so
		// terminals deliver it as "tab" and it can never be told apart from the
		// NextTab binding. Use ctrl+n ("new idea"), a key with its own code.
		CaptureIdea: key.NewBinding(
			key.WithKeys("ctrl+n"),
			key.WithHelp("ctrl+n", "capture idea"),
		),
		Attach: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "attach"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c", "q"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
	}
}
