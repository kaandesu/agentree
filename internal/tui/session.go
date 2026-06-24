package tui

import "time"

// session is a running agent tied to a task, backed by a tmux window.
// Multiple sessions run concurrently in agentree's tmux session; the user
// attaches to interact and tab-cycles between them with tmux's own bindings.
type session struct {
	id       int
	parentID int // 0 for top-level agents
	taskID   int64
	title    string
	kind     string // "agent"
	windowID string // tmux window id, e.g. "@3"
	dir      string // worktree path
	branch   string // agent branch (agentree/<slug>)

	// Project info so completion actions (PR/merge/discard) need no store lookup.
	projectID   int64
	projectName string
	repoPath    string
	baseBranch  string

	// exitedReported guards one-shot agent.exited event emission per session.
	exitedReported bool

	launchAt time.Time
	dead     bool // tmux window's command exited
}

// claudeBuildArgv launches claude to autonomously build a sub-task inside its
// worktree. acceptEdits lets it apply file changes without prompting on every
// edit; the user can attach to handle anything it escalates.
func claudeBuildArgv(prompt string) []string {
	return []string{"claude", prompt, "--permission-mode", "acceptEdits"}
}
