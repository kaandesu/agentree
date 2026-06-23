package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds the lipgloss styles used across the app. Centralizing them keeps
// the look consistent and makes retheming a one-file change.
type Theme struct {
	App         lipgloss.Style
	TabBar      lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	Title       lipgloss.Style
	StatusBar   lipgloss.Style
	Help        lipgloss.Style
	Pane        lipgloss.Style
	PaneFocused lipgloss.Style
	Subtle      lipgloss.Style
	Accent      lipgloss.Style
	ModalBox    lipgloss.Style
}

var (
	colAccent  = lipgloss.Color("212") // pink
	colActive  = lipgloss.Color("87")  // cyan
	colSubtle  = lipgloss.Color("241")
	colBorder  = lipgloss.Color("238")
	colBg      = lipgloss.Color("236")
	colFgLight = lipgloss.Color("255")
)

// NewTheme builds the default theme.
func NewTheme() Theme {
	return Theme{
		App: lipgloss.NewStyle(),
		TabBar: lipgloss.NewStyle().
			Padding(0, 1),
		TabActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(colActive).
			Bold(true).
			Padding(0, 2),
		TabInactive: lipgloss.NewStyle().
			Foreground(colSubtle).
			Padding(0, 2),
		Title: lipgloss.NewStyle().
			Foreground(colAccent).
			Bold(true),
		StatusBar: lipgloss.NewStyle().
			Foreground(colFgLight).
			Background(colBg).
			Padding(0, 1),
		Help: lipgloss.NewStyle().
			Foreground(colSubtle),
		Pane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder),
		PaneFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colActive),
		Subtle: lipgloss.NewStyle().Foreground(colSubtle),
		Accent: lipgloss.NewStyle().Foreground(colAccent),
		ModalBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colAccent).
			Padding(1, 2),
	}
}
