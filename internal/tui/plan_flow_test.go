package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agentree/internal/brain"
	"agentree/internal/orchestrator"
	"agentree/internal/store"
)

// TestSpawnFromProposalCreatesAgents drives the proposal-driven spawning path:
// a PlanProposal with features and sub-tasks is fanned out into worktrees +
// claude build agents. The task lands in `running` and each sub-task becomes
// a worktree. Gated on git + tmux.
func TestSpawnFromProposalCreatesAgents(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if !orchestrator.TmuxAvailable() {
		t.Skip("tmux not available")
	}
	c := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repo := initGitRepo(t)
	proj, _ := st.CreateProject(ctx(), "demo", repo, "main")

	tm := orchestrator.NewTmuxManager(fmt.Sprintf("agentree_proposaltest_%d", os.Getpid()))
	t.Cleanup(func() { _ = tm.Kill(c) })
	if err := tm.Ensure(c); err != nil {
		t.Fatal(err)
	}

	m := Model{
		store: st,
		brain: brain.New(brain.OpenAI, "", ""), // stub
		wt:    orchestrator.NewWorktreeManager(filepath.Join(dir, "worktrees")),
		tmux:  tm,
	}

	proposal := brain.PlanProposal{
		Features: []brain.Feature{
			{
				Title: "Auth",
				SubTasks: []brain.SubTask{
					{Title: "OAuth integration", Prompt: "Build OAuth with Google provider"},
					{Title: "Session management", Prompt: "Build session management with JWT"},
				},
			},
			{
				Title: "Dashboard",
				SubTasks: []brain.SubTask{
					{Title: "Dashboard UI", Prompt: "Build dashboard React components"},
				},
			},
		},
	}

	msg, ok := m.spawnProposalCmd(launchFromProposalMsg{
		project:  *proj,
		proposal: proposal,
		spec:     "Build auth and dashboard",
	})().(planSplitMsg)
	if !ok {
		t.Fatalf("expected planSplitMsg")
	}
	if msg.planSessionID != 0 {
		t.Errorf("planSessionID = %d, want 0", msg.planSessionID)
	}
	if len(msg.spawns) != 3 {
		t.Fatalf("spawns = %d, want 3 (2 auth + 1 dashboard)", len(msg.spawns))
	}
	for _, sp := range msg.spawns {
		if _, err := os.Stat(sp.worktree); err != nil {
			t.Errorf("worktree %q not created: %v", sp.worktree, err)
		}
	}
	if msg.repoPath != repo || msg.baseBranch != "main" {
		t.Errorf("project context not carried: repo=%q base=%q", msg.repoPath, msg.baseBranch)
	}

	got, _ := st.GetTask(ctx(), msg.planTaskID)
	if got.Status != store.StatusRunning {
		t.Errorf("task status = %q, want running", got.Status)
	}
}

// TestStubPlanChatReturnsProposal verifies the stub brain returns a proposal
// without any API calls.
func TestStubPlanChatReturnsProposal(t *testing.T) {
	br := brain.New(brain.OpenAI, "", "") // stub
	resp, err := br.PlanChat(context.Background(), []brain.ChatMessage{
		{Role: "user", Content: "Build a todo app with auth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Proposal == nil {
		t.Fatal("expected proposal from stub, got text response")
	}
	if len(resp.Proposal.Features) != 1 {
		t.Errorf("features = %d, want 1", len(resp.Proposal.Features))
	}
	if resp.Proposal.TotalAgents() < 1 {
		t.Error("expected at least 1 agent")
	}
}

func mustListWindows(t *testing.T, tm *orchestrator.TmuxManager) []orchestrator.Window {
	t.Helper()
	ws, err := tm.ListWindows(context.Background())
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	return ws
}

// initGitRepo creates a committed git repo for worktree tests.
func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# x"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	return repo
}
