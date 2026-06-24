package orchestrator

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FileStat is one changed file in a worktree relative to a base ref.
type FileStat struct {
	Path      string
	Status    string // git status letter: A/M/D/R…, or "?" for untracked
	Additions int    // -1 when unknown (binary or untracked)
	Deletions int    // -1 when unknown
}

// ChangedFiles reports the files a worktree changed relative to base, including
// untracked files (status "?"). Line counts are -1 for binary/untracked files.
// This is the data source for the M6 diff list and the M5 conflict rule.
func ChangedFiles(ctx context.Context, worktreePath, base string) ([]FileStat, error) {
	byPath := map[string]*FileStat{}
	order := []string{}

	// Status letters (tracked changes vs base).
	nameStatus, err := runGit(ctx, "-C", worktreePath, "diff", "--name-status", base)
	if err != nil {
		return nil, fmt.Errorf("git diff --name-status: %w\n%s", err, nameStatus)
	}
	for _, line := range strings.Split(strings.TrimSpace(nameStatus), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		path := fields[len(fields)-1] // handles rename "R100 old new"
		fs := &FileStat{Path: path, Status: fields[0][:1], Additions: -1, Deletions: -1}
		byPath[path] = fs
		order = append(order, path)
	}

	// Line counts (numstat: "<add>\t<del>\t<path>", "-" for binary).
	numstat, err := runGit(ctx, "-C", worktreePath, "diff", "--numstat", base)
	if err != nil {
		return nil, fmt.Errorf("git diff --numstat: %w\n%s", err, numstat)
	}
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		fs := byPath[path]
		if fs == nil {
			fs = &FileStat{Path: path, Status: "M", Additions: -1, Deletions: -1}
			byPath[path] = fs
			order = append(order, path)
		}
		if add, e := strconv.Atoi(parts[0]); e == nil {
			fs.Additions = add
		}
		if del, e := strconv.Atoi(parts[1]); e == nil {
			fs.Deletions = del
		}
	}

	// Untracked files (not yet added) — these are part of the agent's work.
	untracked, err := runGit(ctx, "-C", worktreePath, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files --others: %w\n%s", err, untracked)
	}
	for _, path := range strings.Split(strings.TrimSpace(untracked), "\n") {
		if path == "" {
			continue
		}
		if _, ok := byPath[path]; ok {
			continue
		}
		byPath[path] = &FileStat{Path: path, Status: "?", Additions: -1, Deletions: -1}
		order = append(order, path)
	}

	out := make([]FileStat, 0, len(order))
	for _, p := range order {
		out = append(out, *byPath[p])
	}
	return out, nil
}

// ChangedPaths is the set of paths a worktree touched relative to base. Used by
// the supervisor to detect two agents writing the same files.
func ChangedPaths(ctx context.Context, worktreePath, base string) (map[string]bool, error) {
	files, err := ChangedFiles(ctx, worktreePath, base)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, f := range files {
		set[f.Path] = true
	}
	return set, nil
}

// Shipper performs the outward-facing / destructive completion actions. It is an
// interface so the TUI can inject a fake in tests — `git push` and `gh pr
// create` can't run hermetically against a throwaway repo.
type Shipper interface {
	// Push publishes branch to origin (git push -u origin <branch>).
	Push(ctx context.Context, repo, branch string) error
	// OpenPR opens a pull request via the gh CLI and returns the PR URL.
	OpenPR(ctx context.Context, repo, base, branch string) (url string, err error)
	// Merge merges branch into base with --no-ff, aborting on conflict so the
	// base branch is never left in a half-merged state.
	Merge(ctx context.Context, repo, base, branch string) error
	// Discard removes the worktree and deletes its branch.
	Discard(ctx context.Context, repo, worktreePath, branch string) error
}

// gitShipper is the real Shipper backed by git + the gh CLI.
type gitShipper struct{}

// NewGitShipper returns the production Shipper.
func NewGitShipper() Shipper { return gitShipper{} }

func (gitShipper) Push(ctx context.Context, repo, branch string) error {
	if out, err := runGit(ctx, "-C", repo, "push", "-u", "origin", branch); err != nil {
		return fmt.Errorf("git push: %w\n%s", err, out)
	}
	return nil
}

func (gitShipper) OpenPR(ctx context.Context, repo, base, branch string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("gh CLI not found on PATH — install GitHub CLI to open PRs")
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--base", base, "--head", branch, "--fill")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (gitShipper) Merge(ctx context.Context, repo, base, branch string) error {
	if out, err := runGit(ctx, "-C", repo, "checkout", base); err != nil {
		return fmt.Errorf("checkout %s: %w\n%s", base, err, out)
	}
	if out, err := runGit(ctx, "-C", repo, "merge", "--no-ff", "-m", "agentree: merge "+branch, branch); err != nil {
		// Never leave base half-merged.
		_, _ = runGit(ctx, "-C", repo, "merge", "--abort")
		return fmt.Errorf("merge conflict — aborted; resolve %s manually: %w\n%s", branch, err, out)
	}
	return nil
}

func (gitShipper) Discard(ctx context.Context, repo, worktreePath, branch string) error {
	if out, err := runGit(ctx, "-C", repo, "worktree", "remove", "--force", worktreePath); err != nil {
		return fmt.Errorf("worktree remove: %w\n%s", err, out)
	}
	if out, err := runGit(ctx, "-C", repo, "branch", "-D", branch); err != nil {
		return fmt.Errorf("branch delete: %w\n%s", err, out)
	}
	return nil
}
