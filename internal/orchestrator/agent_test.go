package orchestrator

import (
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until true or the deadline elapses.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestPaneCapturesOutput proves the core PTY→VT pipeline: a real subprocess
// writes to a PTY, the emulator captures it, and onUpdate fires.
func TestPaneCapturesOutput(t *testing.T) {
	var updates int32
	cmd := exec.Command("sh", "-c", "printf 'HELLO_AGENTREE'; sleep 0.3")
	p, err := StartPane(cmd, 80, 24, func() { atomic.AddInt32(&updates, 1) })
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	defer p.Close()

	if !waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(p.Render(), "HELLO_AGENTREE")
	}) {
		t.Fatalf("expected output not captured; render=\n%q", p.Render())
	}
	if atomic.LoadInt32(&updates) == 0 {
		t.Errorf("onUpdate never fired")
	}
}

// TestPaneAltScreen proves we detect when a hosted app switches to the
// alternate screen — the exact behavior of full-screen TUIs like claude/vim.
func TestPaneAltScreen(t *testing.T) {
	// \033[?1049h enters the alternate screen buffer.
	cmd := exec.Command("sh", "-c", "printf '\\033[?1049hALT_ON'; sleep 0.3")
	p, err := StartPane(cmd, 80, 24, nil)
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	defer p.Close()

	if !waitFor(t, 2*time.Second, func() bool { return p.IsAltScreen() }) {
		t.Fatalf("alt-screen not detected; render=\n%q", p.Render())
	}
}

// TestPaneResize proves emulator + PTY resize works (so hosted apps reflow).
func TestPaneResize(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 1")
	p, err := StartPane(cmd, 80, 24, nil)
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	defer p.Close()

	if err := p.Resize(120, 40); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if c, r := p.Size(); c != 120 || r != 40 {
		t.Errorf("size after resize = %dx%d, want 120x40", c, r)
	}
}

// TestPaneInputRoundTrip proves keystrokes forwarded to the PTY reach the
// hosted process: we feed `cat` a line and see it echoed back on screen.
func TestPaneInputRoundTrip(t *testing.T) {
	cmd := exec.Command("cat")
	p, err := StartPane(cmd, 80, 24, nil)
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	defer p.Close()

	if err := p.WriteInput([]byte("PING_PONG\r")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(p.Render(), "PING_PONG")
	}) {
		t.Fatalf("input not echoed; render=\n%q", p.Render())
	}
}
