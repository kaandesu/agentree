package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// WorktreeManager creates and provisions git worktrees under a configured root.
type WorktreeManager struct {
	Root string // e.g. ~/.agentree/worktrees
}

// NewWorktreeManager returns a manager rooted at root.
func NewWorktreeManager(root string) *WorktreeManager { return &WorktreeManager{Root: root} }

// Worktree is the result of provisioning a worktree.
type Worktree struct {
	Path         string   // absolute worktree path
	Branch       string   // created branch name
	LinkedEnv    []string // env files symlinked into the worktree
	BackedUpEnv  []string // env backups created in the source repo
	SetupLabel   string   // install command that ran (empty if none)
	SetupOutput  string   // combined output of the install command
	SetupSkipped bool     // true if no ecosystem detected
}

// branchSlug constraints: lowercase, dashes, alnum.
var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns a free-form title into a filesystem/branch-safe slug.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "task"
	}
	return s
}

// Create provisions a worktree for the given task:
//  1. `git worktree add -b agentree/<slug> <root>/<project>/<slug> <base>`
//  2. backs up the source repo's .env* files to .env.bak (gitignored)
//  3. symlinks those .env* files into the worktree
//  4. auto-detects the ecosystem and runs the install command
//
// baseBranch may be "" to branch from the repo's current HEAD.
func (wm *WorktreeManager) Create(ctx context.Context, repoPath, projectName, taskTitle, baseBranch string) (*Worktree, error) {
	slug := Slugify(taskTitle)
	branch := "agentree/" + slug
	dest := filepath.Join(wm.Root, Slugify(projectName), slug)

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir worktree parent: %w", err)
	}

	args := []string{"-C", repoPath, "worktree", "add", "-b", branch, dest}
	if baseBranch != "" {
		args = append(args, baseBranch)
	}
	if out, err := runGit(ctx, args...); err != nil {
		return nil, fmt.Errorf("git worktree add: %w\n%s", err, out)
	}

	wt := &Worktree{Path: dest, Branch: branch}

	linked, backed, err := backupAndLinkEnv(repoPath, dest)
	if err != nil {
		return wt, fmt.Errorf("env provisioning: %w", err)
	}
	wt.LinkedEnv, wt.BackedUpEnv = linked, backed

	plan, ok, out, setupErr := RunSetup(ctx, dest)
	wt.SetupSkipped = !ok
	if ok {
		wt.SetupLabel = plan.Label
		wt.SetupOutput = out
		if setupErr != nil {
			return wt, fmt.Errorf("setup %q: %w", plan.Label, setupErr)
		}
	}
	return wt, nil
}

// Remove deletes a worktree via git (force) so the branch metadata is cleaned.
func (wm *WorktreeManager) Remove(ctx context.Context, repoPath, worktreePath string) error {
	out, err := runGit(ctx, "-C", repoPath, "worktree", "remove", "--force", worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, out)
	}
	return nil
}

// envGlobs are the env files we manage. .bak and .example are excluded.
func listEnvFiles(repoPath string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(repoPath, ".env*"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		base := filepath.Base(m)
		if strings.HasSuffix(base, ".bak") || strings.HasSuffix(base, ".example") {
			continue
		}
		if info, err := os.Stat(m); err != nil || info.IsDir() {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// backupAndLinkEnv backs up each source .env* to .env*.bak (gitignored) and
// symlinks the original into the worktree so both share one file.
func backupAndLinkEnv(repoPath, worktreePath string) (linked, backed []string, err error) {
	files, err := listEnvFiles(repoPath)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, nil
	}
	if err := ensureGitignored(repoPath, "*.bak"); err != nil {
		return nil, nil, err
	}
	for _, src := range files {
		base := filepath.Base(src)
		bak := src + ".bak"
		if err := copyFile(src, bak); err != nil {
			return linked, backed, fmt.Errorf("backup %s: %w", base, err)
		}
		backed = append(backed, bak)

		link := filepath.Join(worktreePath, base)
		_ = os.Remove(link) // replace any existing
		if err := os.Symlink(src, link); err != nil {
			return linked, backed, fmt.Errorf("symlink %s: %w", base, err)
		}
		linked = append(linked, link)
	}
	return linked, backed, nil
}

// ensureGitignored appends pattern to <repo>/.gitignore if not already present.
func ensureGitignored(repoPath, pattern string) error {
	giPath := filepath.Join(repoPath, ".gitignore")
	data, err := os.ReadFile(giPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil // already ignored
		}
	}
	f, err := os.OpenFile(giPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + "# agentree env backups\n" + pattern + "\n")
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// RepoInfo validates that path is a git work tree and returns a suggested
// project name (the directory base) and the current branch as the default base.
func RepoInfo(ctx context.Context, path string) (name, defaultBranch string, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	if out, err := runGit(ctx, "-C", abs, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", "", fmt.Errorf("not a git repository: %s", strings.TrimSpace(out))
	}
	// symbolic-ref works even on an "unborn" branch (a repo with no commits
	// yet), where `rev-parse --abbrev-ref HEAD` fails with exit 128.
	branch := "main"
	if out, err := runGit(ctx, "-C", abs, "symbolic-ref", "--short", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	} else if out, err := runGit(ctx, "-C", abs, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}
	return filepath.Base(abs), branch, nil
}

func runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
