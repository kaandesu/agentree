package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Add OAuth Login!!":      "add-oauth-login",
		"  spaces  and--dashes ": "spaces-and-dashes",
		"":                       "task",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectSetup(t *testing.T) {
	dir := t.TempDir()
	// pnpm should win over package.json when both present.
	mustWrite(t, filepath.Join(dir, "package.json"), "{}")
	mustWrite(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
	plan, ok := DetectSetup(dir)
	if !ok || plan.Name != "pnpm" {
		t.Fatalf("expected pnpm, got %+v ok=%v", plan, ok)
	}

	goDir := t.TempDir()
	mustWrite(t, filepath.Join(goDir, "go.mod"), "module x")
	plan, ok = DetectSetup(goDir)
	if !ok || plan.Name != "go" {
		t.Fatalf("expected go, got %+v ok=%v", plan, ok)
	}

	if _, ok := DetectSetup(t.TempDir()); ok {
		t.Errorf("expected no detection in empty dir")
	}
}

// TestRepoInfoUnbornBranch verifies RepoInfo works on a freshly-initialized
// repo with no commits yet (where `rev-parse --abbrev-ref HEAD` fails with 128).
func TestRepoInfoUnbornBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	name, branch, err := RepoInfo(context.Background(), dir)
	if err != nil {
		t.Fatalf("RepoInfo on unborn branch: %v", err)
	}
	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}
	if name == "" {
		t.Error("name empty")
	}
}

// TestCreateWorktree exercises the full provisioning path against a real git
// repo: worktree add, .env backup + symlink, gitignore update. Setup install
// is skipped (no recognized ecosystem) to keep the test offline/fast.
func TestCreateWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo := initRepo(t)

	// Source .env to be backed up + linked.
	mustWrite(t, filepath.Join(repo, ".env"), "SECRET=123\n")
	mustWrite(t, filepath.Join(repo, ".env.example"), "SECRET=\n") // must be ignored

	root := t.TempDir()
	wm := NewWorktreeManager(root)
	wt, err := wm.Create(ctx, repo, "demo", "Add OAuth Login", "")
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}

	// Worktree dir + branch.
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree path missing: %v", err)
	}
	if wt.Branch != "agentree/add-oauth-login" {
		t.Errorf("branch = %q", wt.Branch)
	}

	// .env symlinked, .env.example NOT linked.
	link := filepath.Join(wt.Path, ".env")
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".env should be a symlink in worktree (err=%v)", err)
	}
	target, _ := os.Readlink(link)
	if target != filepath.Join(repo, ".env") {
		t.Errorf("symlink target = %q", target)
	}
	if _, err := os.Lstat(filepath.Join(wt.Path, ".env.example")); err == nil {
		t.Errorf(".env.example should not be linked")
	}

	// Backup created in source repo.
	if _, err := os.Stat(filepath.Join(repo, ".env.bak")); err != nil {
		t.Errorf(".env.bak missing in source: %v", err)
	}

	// gitignore contains *.bak.
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if !strings.Contains(string(gi), "*.bak") {
		t.Errorf(".gitignore missing *.bak; got:\n%s", gi)
	}

	if !wt.SetupSkipped {
		t.Errorf("expected setup skipped (no ecosystem); got label=%q", wt.SetupLabel)
	}
}

// --- helpers ---

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func initRepo(t *testing.T) string {
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
	mustWrite(t, filepath.Join(repo, "README.md"), "# demo\n")
	run("add", ".")
	run("commit", "-m", "init")
	return repo
}
