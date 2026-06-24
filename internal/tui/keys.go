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
	CaptureIdea key.Binding
	Quit        key.Binding
	Help        key.Binding
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
		Tab1: key.NewBinding(key.WithKeys("1")),
		Tab2: key.NewBinding(key.WithKeys("2")),
		Tab3: key.NewBinding(key.WithKeys("3")),
		Tab4: key.NewBinding(key.WithKeys("4")),
		Tab5: key.NewBinding(key.WithKeys("5")),
		// ctrl+i is byte 0x09 — identical to Tab in every standard terminal, so
		// terminals deliver it as "tab" and it can never be told apart from the
		// NextTab binding. Use ctrl+n ("new idea"), a key with its own code.
		CaptureIdea: key.NewBinding(
			key.WithKeys("ctrl+n"),
			key.WithHelp("ctrl+n", "capture idea"),
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
