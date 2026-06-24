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
