package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs a git command in dir with deterministic author env.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestChangedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo := initRepo(t)
	wm := NewWorktreeManager(t.TempDir())
	wt, err := wm.Create(ctx, repo, "demo", "change files", "")
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}

	mustWrite(t, filepath.Join(wt.Path, "README.md"), "# demo\nmore lines\n") // modify tracked
	mustWrite(t, filepath.Join(wt.Path, "new.txt"), "brand new\n")            // untracked

	files, err := ChangedFiles(ctx, wt.Path, "main")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	byPath := map[string]FileStat{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if _, ok := byPath["README.md"]; !ok {
		t.Errorf("expected README.md in changes; got %+v", files)
	}
	nf, ok := byPath["new.txt"]
	if !ok {
		t.Fatalf("expected untracked new.txt in changes; got %+v", files)
	}
	if nf.Status != "?" {
		t.Errorf("new.txt status = %q, want ?", nf.Status)
	}
}

func TestMergeNoFF(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo := initRepo(t)
	gitIn(t, repo, "checkout", "-b", "feat")
	mustWrite(t, filepath.Join(repo, "feature.txt"), "hello\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "feature")
	gitIn(t, repo, "checkout", "main")

	if err := NewGitShipper().Merge(ctx, repo, "main", "feat"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Errorf("feature.txt not merged into main: %v", err)
	}
}

func TestMergeConflictAborts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo := initRepo(t)
	mustWrite(t, filepath.Join(repo, "f.txt"), "base\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "add f")

	gitIn(t, repo, "checkout", "-b", "feat")
	mustWrite(t, filepath.Join(repo, "f.txt"), "from-feat\n")
	gitIn(t, repo, "commit", "-am", "feat change")

	gitIn(t, repo, "checkout", "main")
	mustWrite(t, filepath.Join(repo, "f.txt"), "from-main\n")
	gitIn(t, repo, "commit", "-am", "main change")

	err := NewGitShipper().Merge(ctx, repo, "main", "feat")
	if err == nil {
		t.Fatal("expected merge conflict error, got nil")
	}
	// Base must not be left mid-merge.
	if _, statErr := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); statErr == nil {
		t.Error("MERGE_HEAD present — merge was not aborted")
	}
	if data, _ := os.ReadFile(filepath.Join(repo, "f.txt")); string(data) != "from-main\n" {
		t.Errorf("f.txt = %q, want from-main (base unchanged after abort)", data)
	}
}

func TestDiscard(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo := initRepo(t)
	wm := NewWorktreeManager(t.TempDir())
	wt, err := wm.Create(ctx, repo, "demo", "discard me", "")
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}

	if err := NewGitShipper().Discard(ctx, repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree still present after discard: %v", err)
	}
	branches := gitIn(t, repo, "branch", "--list", wt.Branch)
	if branches != "" {
		t.Errorf("branch not deleted: %q", branches)
	}
}
