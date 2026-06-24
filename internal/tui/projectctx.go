package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// buildProjectContext assembles a small, deterministic snapshot of a repo to
// ground the planning LLM: the root entry names, the head of a manifest file
// (go.mod / package.json / …), and the head of the README. Everything is
// best-effort and size-bounded so the context stays cheap and never blocks.
//
// The result is injected as a hidden "system" chat message (the planner only
// renders user/assistant turns), so the model knows what project it's planning
// for without the user pasting anything.
func buildProjectContext(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("PROJECT CONTEXT (auto-collected, read-only) for ")
	b.WriteString(filepath.Base(repoPath))
	b.WriteString(". Use it to ground the plan; ask the user about anything unclear.\n")

	// Root entries (names only — cheap structural signal).
	if entries, err := os.ReadDir(repoPath); err == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if name == ".git" {
				continue
			}
			if e.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) > 0 {
			b.WriteString("\nRoot files & directories:\n")
			b.WriteString(strings.Join(names, "  "))
			b.WriteString("\n")
		}
	}

	// Manifests that exist (language/deps signal).
	manifests := []string{
		"go.mod", "package.json", "pyproject.toml", "Cargo.toml",
		"requirements.txt", "Gemfile", "pom.xml", "build.gradle", "composer.json",
	}
	for _, mf := range manifests {
		if head := readHead(filepath.Join(repoPath, mf), 40, 2000); head != "" {
			b.WriteString("\n--- " + mf + " (head) ---\n")
			b.WriteString(head)
			b.WriteString("\n")
		}
	}

	// README head (first match wins).
	for _, rd := range []string{"README.md", "README.MD", "readme.md", "Readme.md", "README", "README.rst", "README.txt"} {
		if head := readHead(filepath.Join(repoPath, rd), 60, 4000); head != "" {
			b.WriteString("\n--- " + rd + " (head) ---\n")
			b.WriteString(head)
			b.WriteString("\n")
			break
		}
	}
	return b.String()
}

// readHead returns the first n lines of a file, trimmed and capped at maxChars,
// or "" if the file can't be read.
func readHead(path string, n, maxChars int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(out) > maxChars {
		out = out[:maxChars] + "\n…(truncated)"
	}
	return out
}
