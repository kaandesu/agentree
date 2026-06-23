package tui

import (
	"agentree/internal/orchestrator"

	tea "github.com/charmbracelet/bubbletea"
)

// paneModel is the Bubbletea component that hosts an orchestrator.Pane (a PTY
// running an agent CLI) and renders its live screen. When focused, it forwards
// keystrokes to the underlying process.
//
// Redraw signaling uses a coalescing channel: the Pane's read goroutine pokes
// `dirty` (non-blocking, cap 1) and a re-armed listen command turns each poke
// into a paneDirtyMsg so View re-renders. This keeps all PTY I/O off the
// single-threaded Update loop.
type paneModel struct {
	id      int
	pane    *orchestrator.Pane
	dirty   chan struct{}
	focused bool
	w, h    int
}

// paneDirtyMsg / paneExitedMsg carry the pane id so the root can route them to
// the owning session (multiple panes run concurrently).
type paneDirtyMsg struct{ id int }
type paneExitedMsg struct {
	id  int
	err error
}

// newPaneModel starts cmd on a PTY and returns a component sized cols×rows. id
// tags the pane's redraw/exit messages for routing.
func newPaneModel(id int, start func(onUpdate func(), cols, rows int) (*orchestrator.Pane, error), cols, rows int) (*paneModel, error) {
	m := &paneModel{id: id, dirty: make(chan struct{}, 1)}
	poke := func() {
		select {
		case m.dirty <- struct{}{}:
		default:
		}
	}
	p, err := start(poke, cols, rows)
	if err != nil {
		return nil, err
	}
	m.pane = p
	m.w, m.h = cols, rows
	return m, nil
}

// listen blocks for the next redraw poke and converts it to a message.
func (m *paneModel) listen() tea.Cmd {
	return func() tea.Msg {
		<-m.dirty
		if exited, err := m.pane.Exited(); exited {
			return paneExitedMsg{id: m.id, err: err}
		}
		return paneDirtyMsg{id: m.id}
	}
}

func (m *paneModel) Init() tea.Cmd { return m.listen() }

func (m *paneModel) SetSize(w, h int) {
	m.w, m.h = w, h
	if m.pane != nil {
		_ = m.pane.Resize(w, h)
	}
}

func (m *paneModel) Update(msg tea.Msg) (*paneModel, tea.Cmd) {
	switch msg := msg.(type) {
	case paneDirtyMsg:
		if msg.id == m.id {
			return m, m.listen() // re-arm; View renders the fresh screen
		}
		return m, nil
	case paneExitedMsg:
		return m, nil
	case tea.KeyMsg:
		if m.focused && m.pane != nil {
			if b := keyToBytes(msg); b != nil {
				_ = m.pane.WriteInput(b)
			}
		}
		return m, nil
	}
	return m, nil
}

func (m *paneModel) View() string {
	if m.pane == nil {
		return ""
	}
	return m.pane.Render()
}

func (m *paneModel) Close() error {
	if m.pane == nil {
		return nil
	}
	return m.pane.Close()
}

// keyToBytes reconstructs the raw byte sequence for a Bubbletea key so it can
// be forwarded to the hosted PTY process. Covers printable runes, common
// editing/navigation keys, and Ctrl combinations.
func keyToBytes(k tea.KeyMsg) []byte {
	switch k.Type {
	case tea.KeyRunes:
		b := []byte(string(k.Runes))
		if k.Alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	case tea.KeySpace:
		return []byte(" ")
	case tea.KeyEnter:
		return []byte("\r")
	case tea.KeyTab:
		return []byte("\t")
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEsc:
		return []byte{0x1b}
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	}
	// Ctrl combinations: Bubbletea's control KeyTypes equal their ASCII control
	// codes (Ctrl-A == 1 … Ctrl-Z == 26, etc.). Forward those directly.
	if t := int(k.Type); t > 0 && t < 32 {
		return []byte{byte(t)}
	}
	return nil
}
