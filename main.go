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
	"path/filepath"
	"strings"

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

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err = p.Run()
	return err
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
