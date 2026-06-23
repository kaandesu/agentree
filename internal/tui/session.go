package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"agentree/internal/orchestrator"
)

// session is a running agent/plan pane tied to a task. Multiple sessions run
// concurrently; the root model owns them and shows one fullscreen at a time.
type session struct {
	id       int
	taskID   int64
	title    string
	kind     string // "plan" | "agent"
	pane     *paneModel
	worktree string

	// Plan-artifact ingestion (best-effort). We snapshot the global plans dir
	// at launch and, on session exit, attribute any newer file to this session.
	plansDir string
	snapshot map[string]bool
	launchAt time.Time
	planPath string // discovered plan file
	exited   bool
	ingested bool
}

// plansDir returns ~/.claude/plans, where Claude Code persists plan files.
func plansDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "plans")
}

// snapshotPlans records the set of filenames currently in dir.
func snapshotPlans(dir string) map[string]bool {
	set := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return set
	}
	for _, e := range entries {
		if !e.IsDir() {
			set[e.Name()] = true
		}
	}
	return set
}

// findNewPlan returns the newest plan file in dir that is absent from snapshot
// and modified at/after launchAt. Empty string if none.
func findNewPlan(dir string, snapshot map[string]bool, launchAt time.Time) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || snapshot[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(launchAt.Add(-1 * time.Second)) {
			continue
		}
		cands = append(cands, cand{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	return cands[0].path
}

// startAgentPane launches argv on a PTY in dir and wraps it as a pane.
func startAgentPane(id int, dir string, argv []string, w, h int) (*paneModel, error) {
	start := func(onUpdate func(), cols, rows int) (*orchestrator.Pane, error) {
		c := exec.Command(argv[0], argv[1:]...)
		c.Dir = dir
		c.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
		return orchestrator.StartPane(c, cols, rows, onUpdate)
	}
	return newPaneModel(id, start, w, h)
}

// claudePlanArgv builds the command to launch claude in interactive plan mode
// seeded with the expanded prompt.
func claudePlanArgv(prompt string) []string {
	return []string{"claude", prompt, "--permission-mode", "plan"}
}
