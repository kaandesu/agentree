package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestTmuxManager exercises the real tmux scripting path on a throwaway socket.
// It is hermetic (own socket, killed in cleanup) and skips when tmux is absent.
func TestTmuxManager(t *testing.T) {
	if !TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	tm := NewTmuxManager(fmt.Sprintf("agentree_test_%d", os.Getpid()))
	t.Cleanup(func() { _ = tm.Kill(ctx) })

	if err := tm.Ensure(ctx); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := tm.Ensure(ctx); err != nil { // idempotent
		t.Fatalf("Ensure (2nd): %v", err)
	}

	// A long-lived window we can capture while alive.
	dir := t.TempDir()
	live, err := tm.NewWindow(ctx, "live-probe", dir,
		[]string{"sh", "-c", "echo HELLO_TMUX; sleep 30"})
	if err != nil {
		t.Fatalf("NewWindow(live): %v", err)
	}
	if !strings.HasPrefix(live, "@") {
		t.Errorf("window id = %q, want @-prefixed", live)
	}
	if got := waitForCapture(t, tm, live, "HELLO_TMUX"); !strings.Contains(got, "HELLO_TMUX") {
		t.Errorf("capture never showed marker; got:\n%s", got)
	}

	// A short-lived window that exits non-zero: remain-on-exit must retain it
	// as a dead window carrying the exit status.
	dead, err := tm.NewWindow(ctx, "dead-probe", dir,
		[]string{"sh", "-c", "echo BYE; exit 7"})
	if err != nil {
		t.Fatalf("NewWindow(dead): %v", err)
	}
	w := waitForDead(t, tm, dead)
	if w.Status != 7 {
		t.Errorf("dead window status = %d, want 7", w.Status)
	}

	// Both windows should be listed (plus the base shell window).
	wins, err := tm.ListWindows(ctx)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if !hasWindow(wins, live) || !hasWindow(wins, dead) {
		t.Errorf("ListWindows missing spawned windows; got %+v", wins)
	}

	// Kill the live window and confirm it disappears.
	if err := tm.KillWindow(ctx, live); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}
	wins, _ = tm.ListWindows(ctx)
	if hasWindow(wins, live) {
		t.Errorf("killed window still present; got %+v", wins)
	}
}

// TestInheritedTmuxManager exercises inherited mode: agents are spawned as panes
// in agentree's own window. It stands up a throwaway tmux server, points $TMUX at
// it (so the manager's `tmux` calls — which omit -L — target that server, exactly
// as they would inside a real session), and verifies split/list/kill on panes.
func TestInheritedTmuxManager(t *testing.T) {
	if !TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	sock := fmt.Sprintf("agentree_itest_%d", os.Getpid())

	// Boot a server with a session standing in for agentree's own pane.
	probe := &TmuxManager{Socket: sock, Session: "agentree"}
	if _, err := probe.tmux(ctx, "new-session", "-d", "-s", "agentree", "-x", "200", "-y", "50", "sleep", "30"); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	t.Cleanup(func() { _, _ = probe.tmux(ctx, "kill-server") })

	info, err := probe.tmux(ctx, "display-message", "-p", "-t", "agentree",
		"#{socket_path},#{pid},#{session_id}|#{pane_id}")
	if err != nil {
		t.Fatalf("display-message: %v", err)
	}
	parts := strings.SplitN(strings.TrimSpace(info), "|", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected display output: %q", info)
	}
	// $TMUX is "socket_path,server_pid,session_id" with the leading $ stripped.
	t.Setenv("TMUX", strings.Replace(parts[0], "$", "", 1))
	t.Setenv("TMUX_PANE", parts[1])

	tm := NewInheritedTmuxManager(false)
	if !tm.Inherited() {
		t.Fatal("expected inherited mode")
	}
	if err := tm.Ensure(ctx); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	dir := t.TempDir()
	pane, err := tm.NewWindow(ctx, "agent-probe", dir, []string{"sh", "-c", "echo HELLO_PANE; sleep 30"})
	if err != nil {
		t.Fatalf("NewWindow(split): %v", err)
	}
	if !strings.HasPrefix(pane, "%") {
		t.Errorf("pane id = %q, want %%-prefixed", pane)
	}
	if got := waitForCapture(t, tm, pane, "HELLO_PANE"); !strings.Contains(got, "HELLO_PANE") {
		t.Errorf("capture never showed marker; got:\n%s", got)
	}

	// The agent pane is listed; agentree's own pane is filtered out.
	wins := mustList(t, tm)
	if !hasWindow(wins, pane) {
		t.Errorf("ListWindows missing agent pane; got %+v", wins)
	}
	if hasWindow(wins, parts[1]) {
		t.Errorf("ListWindows should exclude agentree's own pane %s; got %+v", parts[1], wins)
	}

	if err := tm.KillWindow(ctx, pane); err != nil {
		t.Fatalf("KillWindow(pane): %v", err)
	}
	if hasWindow(mustList(t, tm), pane) {
		t.Errorf("killed pane still present")
	}
}

func waitForCapture(t *testing.T, tm *TmuxManager, id, marker string) string {
	t.Helper()
	var out string
	for i := 0; i < 40; i++ {
		out, _ = tm.Capture(context.Background(), id)
		if strings.Contains(out, marker) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	return out
}

func waitForDead(t *testing.T, tm *TmuxManager, id string) Window {
	t.Helper()
	for i := 0; i < 40; i++ {
		for _, w := range mustList(t, tm) {
			if w.ID == id && w.Dead {
				return w
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("window %s never became dead", id)
	return Window{}
}

func mustList(t *testing.T, tm *TmuxManager) []Window {
	t.Helper()
	ws, err := tm.ListWindows(context.Background())
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	return ws
}

func hasWindow(ws []Window, id string) bool {
	for _, w := range ws {
		if w.ID == id {
			return true
		}
	}
	return false
}
