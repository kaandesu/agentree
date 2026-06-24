package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agentree/internal/brain"
	"agentree/internal/orchestrator"
	"agentree/internal/store"
)

// TestPreparePlanCreatesTask exercises the non-PTY half of the planning flow:
// a task is created in `planning` and the spec is expanded. No worktree is
// created here — planning runs read-only in the repo until the plan is split.
func TestPreparePlanCreatesTask(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.CreateProject(ctx(), "demo", "/tmp/demo", "main")
	if err != nil {
		t.Fatal(err)
	}

	m := Model{
		store: st,
		brain: brain.New(brain.OpenAI, "", ""), // stub
	}

	msg := m.preparePlanCmd(launchPlanRequestMsg{
		project: *proj,
		spec:    "Build OAuth login\nwith Google and GitHub",
	})()

	prepared, ok := msg.(planPreparedMsg)
	if !ok {
		t.Fatalf("expected planPreparedMsg, got %T: %+v", msg, msg)
	}
	if prepared.title != "Build OAuth login" {
		t.Errorf("title = %q", prepared.title)
	}
	if prepared.repoPath != "/tmp/demo" {
		t.Errorf("repoPath = %q", prepared.repoPath)
	}
	if prepared.expanded == "" {
		t.Errorf("spec was not expanded")
	}

	task, err := st.GetTask(ctx(), prepared.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != store.StatusPlanning {
		t.Errorf("task status = %q, want planning", task.Status)
	}
}

// TestIngestSplitReadsPlanAndSpawns drives the full ingest+split path against a
// real git repo and tmux server: the plan file is read, the task moves to
// ready, and (with the stub brain → single sub-task) a worktree + agent window
// are provisioned. Gated on git + tmux.
func TestIngestSplitReadsPlanAndSpawns(t *testing.T) {
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
	task, _ := st.CreateTask(ctx(), store.Task{ProjectID: proj.ID, Title: "do thing", Status: store.StatusPlanning})

	plans := t.TempDir()
	launch := time.Now()
	snap := snapshotPlans(plans)
	planFile := filepath.Join(plans, "do-thing-clever-turing.md")
	if err := os.WriteFile(planFile, []byte("# Plan\n1. build the thing"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(planFile, launch.Add(time.Second), launch.Add(time.Second))

	tm := orchestrator.NewTmuxManager(fmt.Sprintf("agentree_test_%d", os.Getpid()))
	t.Cleanup(func() { _ = tm.Kill(c) })
	if err := tm.Ensure(c); err != nil {
		t.Fatal(err)
	}
	planWin, err := tm.NewWindow(c, "plan", repo, []string{"sh", "-c", "sleep 30"})
	if err != nil {
		t.Fatal(err)
	}

	m := Model{
		store: st,
		brain: brain.New(brain.OpenAI, "", ""), // stub → single sub-task
		wt:    orchestrator.NewWorktreeManager(filepath.Join(dir, "worktrees")),
		tmux:  tm,
	}
	s := &session{
		id: 1, taskID: task.ID, kind: "plan", windowID: planWin,
		dir: repo, repoPath: repo, projectName: "demo", baseBranch: "main",
		plansDir: plans, snapshot: snap, launchAt: launch,
	}

	msg, ok := m.ingestSplitCmd(s)().(planSplitMsg)
	if !ok {
		t.Fatalf("expected planSplitMsg")
	}
	if msg.planPath != planFile {
		t.Errorf("planPath = %q, want %q", msg.planPath, planFile)
	}
	if len(msg.spawns) != 1 {
		t.Fatalf("spawns = %d, want 1", len(msg.spawns))
	}
	if _, err := os.Stat(msg.spawns[0].worktree); err != nil {
		t.Errorf("worktree not created: %v", err)
	}

	// The planning window must have been killed before fan-out.
	for _, w := range mustListWindows(t, tm) {
		if w.ID == planWin {
			t.Errorf("planning window %s should have been killed", planWin)
		}
	}

	got, _ := st.GetTask(ctx(), task.ID)
	if got.Status != store.StatusReady {
		t.Errorf("task status = %q, want ready", got.Status)
	}
	if got.PlanPath != planFile {
		t.Errorf("task plan path = %q, want %q", got.PlanPath, planFile)
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

func TestFindNewPlanPicksNewestNew(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.md")
	os.WriteFile(old, []byte("x"), 0o644)
	snap := snapshotPlans(dir) // contains old.md

	launch := time.Now()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	os.WriteFile(a, []byte("x"), 0o644)
	os.WriteFile(b, []byte("x"), 0o644)
	os.Chtimes(a, launch.Add(time.Second), launch.Add(time.Second))
	os.Chtimes(b, launch.Add(2*time.Second), launch.Add(2*time.Second))

	if got := findNewPlan(dir, snap, launch); got != b {
		t.Errorf("findNewPlan = %q, want %q (newest new file)", got, b)
	}
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
