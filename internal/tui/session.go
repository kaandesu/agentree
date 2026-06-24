package tui

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// session is a running plan/agent tied to a task, backed by a tmux window.
// Multiple sessions run concurrently in agentree's tmux session; the user
// attaches to interact and tab-cycles between them with tmux's own bindings.
type session struct {
	id       int
	taskID   int64
	title    string
	kind     string // "plan" | "agent"
	windowID string // tmux window id, e.g. "@3"
	dir      string // repo path (plan) or worktree path (agent)

	// Project info carried on plan sessions so the split step can provision a
	// worktree per sub-task without re-reading the store.
	projectID   int64
	projectName string
	repoPath    string
	baseBranch  string

	// Plan-artifact ingestion (plan sessions). We snapshot the global plans dir
	// at launch and attribute any newer file to this session.
	plansDir string
	snapshot map[string]bool
	launchAt time.Time
	planPath string // discovered plan file

	dead      bool // tmux window's command exited
	splitting bool // an ingest+split command is in flight
	ingested  bool // plan captured and split kicked off (terminal for plan)
	failed    bool // plan window died without ever producing a plan (terminal)
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

// claudePlanArgv builds the command to launch claude in interactive plan mode
// seeded with the expanded prompt. Plan mode is read-only, so it's safe to run
// directly in the repo (no worktree needed until the plan is split).
func claudePlanArgv(prompt string) []string {
	return []string{"claude", prompt, "--permission-mode", "plan"}
}

// claudeBuildArgv launches claude to autonomously build a sub-task inside its
// worktree. acceptEdits lets it apply file changes without prompting on every
// edit; the user can attach to handle anything it escalates.
func claudeBuildArgv(prompt string) []string {
	return []string{"claude", prompt, "--permission-mode", "acceptEdits"}
}
