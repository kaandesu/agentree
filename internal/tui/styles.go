package tui

import "github.com/charmbracelet/lipgloss"

// Theme holds the lipgloss styles used across the app. Centralizing them keeps
// the look consistent and makes retheming a one-file change.
type Theme struct {
	App         lipgloss.Style
	TabBar      lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
	Sidebar     lipgloss.Style
	SidebarLogo lipgloss.Style
	NavActive   lipgloss.Style
	NavInactive lipgloss.Style
	NavBadge    lipgloss.Style
	PageHeader  lipgloss.Style
	ContentPane lipgloss.Style
	Title       lipgloss.Style
	StatusBar   lipgloss.Style
	Help        lipgloss.Style
	Pane        lipgloss.Style
	PaneFocused lipgloss.Style
	Subtle      lipgloss.Style
	Accent      lipgloss.Style
	ModalBox    lipgloss.Style
	// Semantic status styles (centralized so the look is consistent and
	// retheming is a one-file change).
	StatusOk   lipgloss.Style // running / healthy
	StatusWarn lipgloss.Style // ready / review / attention
	StatusErr  lipgloss.Style // errored exit
	StatusIdle lipgloss.Style // done / exited cleanly / inactive
	Tree       lipgloss.Style // tree connector glyphs
}

var (
	colAccent  = lipgloss.Color("212") // pink
	colActive  = lipgloss.Color("87")  // cyan
	colSubtle  = lipgloss.Color("241")
	colBorder  = lipgloss.Color("238")
	colBg      = lipgloss.Color("236")
	colFgLight = lipgloss.Color("255")
	colOk      = lipgloss.Color("82")  // green
	colWarn    = lipgloss.Color("220") // yellow
	colErr     = lipgloss.Color("196") // red
)

func fitDim(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

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
		Sidebar: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder).
			Padding(1, 1),
		SidebarLogo: lipgloss.NewStyle().
			Foreground(colActive).
			Bold(true),
		NavActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(colActive).
			Bold(true).
			Padding(0, 1),
		NavInactive: lipgloss.NewStyle().
			Foreground(colFgLight).
			Padding(0, 1),
		NavBadge: lipgloss.NewStyle().
			Foreground(colActive).
			Bold(true),
		PageHeader: lipgloss.NewStyle().
			Foreground(colFgLight).
			Bold(true).
			Padding(0, 1),
		ContentPane: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colBorder),
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
		StatusOk:   lipgloss.NewStyle().Foreground(colOk),
		StatusWarn: lipgloss.NewStyle().Foreground(colWarn),
		StatusErr:  lipgloss.NewStyle().Foreground(colErr),
		StatusIdle: lipgloss.NewStyle().Foreground(colSubtle),
		Tree:       lipgloss.NewStyle().Foreground(colBorder),
	}
}
