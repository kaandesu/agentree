// Command agentree is a TUI "smart hypervisor" that orchestrates parallel git
// worktrees and external agent CLIs (claude/codex), with an LLM brain for idea
// triage and scheduling. See ~/.claude/plans for the design.
//
// Usage:
//
//	agentree                 # open the TUI
//	agentree .               # register/focus the repo at the given path
//	agentree /path/to/repo   # same, with an explicit path
//	agentree spike -- claude # host a single agent CLI in an embedded pane
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"agentree/internal/config"
	"agentree/internal/orchestrator"
	"agentree/internal/store"
	"agentree/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentree:", err)
		os.Exit(1)
	}
}

func run() error {
	// `agentree spike -- <cmd...>` launches a single embedded pane to manually
	// validate hosting a real agent TUI (claude/codex). See tui.RunSpike.
	if len(os.Args) > 1 && os.Args[1] == "spike" {
		args := os.Args[2:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		return tui.RunSpike(args)
	}

	// agentree is an agent multiplexer: it shows its own dashboard beside the
	// agent panes it spawns. For that it must itself be a tmux pane, so when
	// launched from a plain terminal we re-exec inside a fresh tmux session
	// (pane 0). $TMUX in the child stops this from looping; if we're already in
	// tmux (the user's own session or our re-exec) we skip and use that server.
	if err := bootstrapTmux(); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Optional positional repo-path argument: register (idempotently) and focus
	// the Planner on it. Anything starting with "-" is left for future flags.
	var initProject *store.Project
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		p, err := registerRepoArg(st, os.Args[1])
		if err != nil {
			return err
		}
		initProject = p
	}

	model := tui.New(cfg, st)
	if initProject != nil {
		model = model.WithInitialProject(*initProject)
	}

	// No mouse capture: agentree runs inside tmux, so tmux owns the mouse for
	// pane select/resize. The dashboard is keyboard-driven (j/k) anyway.
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// bootstrapTmux re-execs agentree inside a fresh tmux session when it isn't
// already running in tmux, so it can sit as a pane alongside the agent panes it
// spawns. It is a no-op (returns nil to continue normally) when already in tmux,
// when tmux isn't installed, or when stdout isn't a terminal (tests/pipes) — in
// those cases agentree runs as a plain full-screen TUI.
func bootstrapTmux() error {
	if os.Getenv("TMUX") != "" { // already a pane (user's session or our re-exec)
		return nil
	}
	if !orchestrator.TmuxAvailable() || !isTerminal(os.Stdout) {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil // fall back to the plain TUI rather than failing to start
	}
	socket := "agentree"
	if cfg, err := config.Load(); err == nil && strings.TrimSpace(cfg.TmuxSocket) != "" {
		socket = cfg.TmuxSocket
	}
	// new-session -A creates-or-attaches a session named "agentree" running this
	// binary as pane 0. AGENTREE_OWNS_TMUX tells the child it created this server
	// (so it may flip session-global options like mouse).
	argv := []string{"tmux", "-L", socket, "new-session", "-A", "-s", "agentree", "--", self}
	argv = append(argv, os.Args[1:]...)
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return nil
	}
	env := append(os.Environ(), "AGENTREE_OWNS_TMUX=1")
	// Replace this process with tmux; the child agentree inherits a set $TMUX and
	// skips this bootstrap.
	return syscall.Exec(tmuxPath, argv, env)
}

// isTerminal reports whether f is a character device (a TTY), used to avoid
// re-execing into tmux when output is piped or captured.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// registerRepoArg resolves arg to an absolute path, validates it's a git repo,
// and returns the existing-or-created project record.
func registerRepoArg(st *store.Store, arg string) (*store.Project, error) {
	abs, err := filepath.Abs(arg)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	name, branch, err := orchestrator.RepoInfo(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("register %q: %w", arg, err)
	}
	return st.GetOrCreateProject(ctx, name, abs, branch)
}
