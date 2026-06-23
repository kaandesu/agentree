// Package orchestrator manages worktrees and the external agent CLIs that run
// inside them. The Pane type is the keystone: it runs a command on a PTY and
// feeds its output through a virtual-terminal emulator so a full-screen,
// alt-screen TUI (like `claude`) can be hosted and rendered inside our own
// Bubbletea UI.
package orchestrator

import (
	"os"
	"os/exec"
	"sync"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Pane hosts a single command on a PTY with an attached VT emulator. It is
// framework-agnostic (no Bubbletea types) so it can be unit-tested directly;
// the TUI wraps it as a focusable component.
type Pane struct {
	emu  *vt.SafeEmulator
	ptmx *os.File
	cmd  *exec.Cmd

	mu      sync.Mutex
	cols    int
	rows    int
	exited  bool
	exitErr error

	// onUpdate fires (from the read goroutine) whenever the screen changes or
	// the process exits. The TUI uses it to signal a redraw.
	onUpdate func()
}

// StartPane launches cmd on a PTY sized cols×rows and begins streaming its
// output into a VT emulator. onUpdate may be nil.
func StartPane(cmd *exec.Cmd, cols, rows int, onUpdate func()) (*Pane, error) {
	emu := vt.NewSafeEmulator(cols, rows)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	p := &Pane{
		emu:      emu,
		ptmx:     ptmx,
		cmd:      cmd,
		cols:     cols,
		rows:     rows,
		onUpdate: onUpdate,
	}
	go p.readLoop()
	return p, nil
}

func (p *Pane) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			_, _ = p.emu.Write(buf[:n])
			p.notify()
		}
		if err != nil {
			p.mu.Lock()
			p.exited = true
			p.exitErr = err
			p.mu.Unlock()
			p.notify()
			return
		}
	}
}

func (p *Pane) notify() {
	if p.onUpdate != nil {
		p.onUpdate()
	}
}

// Render returns the current screen as an ANSI string suitable for printing.
func (p *Pane) Render() string { return p.emu.Render() }

// IsAltScreen reports whether the hosted app has switched to the alternate
// screen buffer (true for full-screen TUIs like claude/vim).
func (p *Pane) IsAltScreen() bool { return p.emu.IsAltScreen() }

// Size returns the current emulator dimensions.
func (p *Pane) Size() (cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cols, p.rows
}

// Resize resizes both the emulator and the underlying PTY so the hosted app
// reflows correctly.
func (p *Pane) Resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return nil
	}
	p.mu.Lock()
	p.cols, p.rows = cols, rows
	p.mu.Unlock()
	p.emu.Resize(cols, rows)
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// WriteInput forwards raw input bytes (keystrokes) to the hosted process.
func (p *Pane) WriteInput(b []byte) error {
	_, err := p.ptmx.Write(b)
	return err
}

// Exited reports whether the process has ended and the read error (usually EOF
// / "input/output error" on PTY close).
func (p *Pane) Exited() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited, p.exitErr
}

// Wait blocks until the process exits, returning its exit error.
func (p *Pane) Wait() error { return p.cmd.Wait() }

// Close terminates the process and releases the PTY.
func (p *Pane) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.ptmx.Close()
}
