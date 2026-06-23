package orchestrator

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestLiveClaudeCapture is an opt-in integration test that runs the REAL claude
// binary on a Pane and asserts its output is captured by the VT emulator. It
// makes a real API call, so it is skipped unless AGENTREE_LIVE=1 (and claude is
// on PATH). Run with:
//
//	AGENTREE_LIVE=1 go test ./internal/orchestrator/ -run TestLiveClaudeCapture -v
func TestLiveClaudeCapture(t *testing.T) {
	if os.Getenv("AGENTREE_LIVE") != "1" {
		t.Skip("set AGENTREE_LIVE=1 to run the live claude capture test")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	// -p (print mode) emits plain text and exits, so the emulator retains it.
	cmd := exec.Command("claude", "-p", "Reply with exactly the single word BANANA and nothing else.")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	p, err := StartPane(cmd, 100, 30, nil)
	if err != nil {
		t.Fatalf("start pane: %v", err)
	}
	defer p.Close()

	if !waitFor(t, 60*time.Second, func() bool {
		return strings.Contains(strings.ToUpper(p.Render()), "BANANA")
	}) {
		t.Fatalf("did not capture claude output; render=\n%s", p.Render())
	}
	t.Logf("captured real claude output through the VT pipeline ✓")
}
