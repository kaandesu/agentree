package tui

import (
	"fmt"
	"os"
	"os/exec"

	"agentree/internal/orchestrator"

	tea "github.com/charmbracelet/bubbletea"
)

// RunSpike launches a single full-screen embedded pane running the given
// command. It exists to manually validate the M2 keystone: hosting a real
// full-screen agent TUI (e.g. `claude`, `codex`) nested inside our Bubbletea
// app via the VT emulator. Quit with Ctrl-\.
//
//	go run . spike -- claude
//	go run . spike -- codex
func RunSpike(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("spike: provide a command, e.g. `agentree spike -- claude`")
	}
	m := &spikeModel{args: args}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

type spikeModel struct {
	args []string
	pane *paneModel
	err  error
	w, h int
}

func (m *spikeModel) Init() tea.Cmd { return nil }

func (m *spikeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.pane == nil {
			pane, err := newPaneModel(0, m.startCmd, m.w, m.h)
			if err != nil {
				m.err = err
				return m, tea.Quit
			}
			pane.focused = true
			m.pane = pane
			return m, m.pane.Init()
		}
		m.pane.SetSize(m.w, m.h)
		return m, nil

	case tea.KeyMsg:
		// Ctrl-\ is our escape hatch to quit the spike harness.
		if msg.Type == tea.KeyCtrlBackslash {
			if m.pane != nil {
				_ = m.pane.Close()
			}
			return m, tea.Quit
		}
		if m.pane != nil {
			_, cmd := m.pane.Update(msg)
			return m, cmd
		}
		return m, nil

	case paneExitedMsg:
		if m.pane != nil {
			_ = m.pane.Close()
		}
		return m, tea.Quit
	}

	if m.pane != nil {
		_, cmd := m.pane.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *spikeModel) View() string {
	if m.err != nil {
		return "spike error: " + m.err.Error() + "\n"
	}
	if m.pane == nil {
		return "starting…"
	}
	return m.pane.View()
}

// startCmd builds the child process for the pane with a terminal-friendly env.
func (m *spikeModel) startCmd(onUpdate func(), cols, rows int) (*orchestrator.Pane, error) {
	c := exec.Command(m.args[0], m.args[1:]...)
	c.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	return orchestrator.StartPane(c, cols, rows, onUpdate)
}
