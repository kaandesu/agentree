package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agentree/internal/brain"
	"agentree/internal/orchestrator"
	"agentree/internal/store"
)

// TestPreparePlanCreatesTaskAndWorktree exercises the non-PTY half of the
// planning flow: a task is created in `planning`, a worktree is provisioned,
// and the spec is expanded. (Launching claude needs a TTY, so it's excluded.)
func TestPreparePlanCreatesTaskAndWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repo := initGitRepo(t)
	proj, err := st.CreateProject(ctx(), "demo", repo, "main")
	if err != nil {
		t.Fatal(err)
	}

	m := Model{
		store: st,
		brain: brain.New(brain.OpenAI, "", ""), // stub
		wt:    orchestrator.NewWorktreeManager(filepath.Join(dir, "worktrees")),
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
	if _, err := os.Stat(prepared.worktree); err != nil {
		t.Errorf("worktree not created: %v", err)
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

// TestPlanIngestionOnExit verifies that when a plan session's pane exits, a new
// plan file appearing after launch is ingested and the task moves to ready.
func TestPlanIngestionOnExit(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, _ := st.CreateProject(ctx(), "demo", "/tmp/x", "main")
	task, _ := st.CreateTask(ctx(), store.Task{ProjectID: proj.ID, Title: "do thing", Status: store.StatusPlanning})

	plans := t.TempDir()
	launch := time.Now()
	snap := snapshotPlans(plans) // empty

	// Simulate claude writing the plan file after launch.
	planFile := filepath.Join(plans, "do-thing-clever-turing.md")
	if err := os.WriteFile(planFile, []byte("# Plan\n1. step"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure mtime is clearly after launch.
	_ = os.Chtimes(planFile, launch.Add(time.Second), launch.Add(time.Second))

	m := Model{store: st, sessions: map[int]*session{
		7: {id: 7, taskID: task.ID, kind: "plan", plansDir: plans, snapshot: snap, launchAt: launch},
	}}

	if _, cmd := (&m).handlePaneExited(paneExitedMsg{id: 7}); cmd == nil {
		t.Error("expected a refresh command")
	}

	got, _ := st.GetTask(ctx(), task.ID)
	if got.Status != store.StatusReady {
		t.Errorf("status = %q, want ready", got.Status)
	}
	if got.PlanPath != planFile {
		t.Errorf("plan path = %q, want %q", got.PlanPath, planFile)
	}
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
