package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// SetupPlan describes how to install dependencies for a detected ecosystem.
type SetupPlan struct {
	Label string   // human label, e.g. "pnpm install"
	Name  string   // executable, e.g. "pnpm"
	Args  []string // arguments
}

// detectors are checked in order; the first lockfile/marker found wins. Order
// matters: more specific lockfiles (pnpm, yarn, bun) precede package-lock.
var detectors = []struct {
	marker string
	plan   SetupPlan
}{
	{"pnpm-lock.yaml", SetupPlan{"pnpm install", "pnpm", []string{"install"}}},
	{"yarn.lock", SetupPlan{"yarn install", "yarn", []string{"install"}}},
	{"bun.lockb", SetupPlan{"bun install", "bun", []string{"install"}}},
	{"bun.lock", SetupPlan{"bun install", "bun", []string{"install"}}},
	{"package-lock.json", SetupPlan{"npm ci", "npm", []string{"ci"}}},
	{"package.json", SetupPlan{"npm install", "npm", []string{"install"}}},
	{"go.mod", SetupPlan{"go mod download", "go", []string{"mod", "download"}}},
	{"requirements.txt", SetupPlan{"pip install -r requirements.txt", "pip", []string{"install", "-r", "requirements.txt"}}},
	{"poetry.lock", SetupPlan{"poetry install", "poetry", []string{"install"}}},
	{"Cargo.toml", SetupPlan{"cargo fetch", "cargo", []string{"fetch"}}},
	{"Gemfile", SetupPlan{"bundle install", "bundle", []string{"install"}}},
}

// DetectSetup inspects dir for known lockfiles/markers and returns the install
// plan. ok is false if no ecosystem is recognized.
func DetectSetup(dir string) (plan SetupPlan, ok bool) {
	for _, d := range detectors {
		if fileExists(filepath.Join(dir, d.marker)) {
			return d.plan, true
		}
	}
	return SetupPlan{}, false
}

// RunSetup detects and runs the install command in dir, returning the plan that
// was run and combined output. If no ecosystem is detected, ok is false and no
// command runs. If the tool is missing from PATH, the error is returned.
func RunSetup(ctx context.Context, dir string) (plan SetupPlan, ok bool, output string, err error) {
	plan, ok = DetectSetup(dir)
	if !ok {
		return plan, false, "", nil
	}
	cmd := exec.CommandContext(ctx, plan.Name, plan.Args...)
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()
	return plan, true, string(out), runErr
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
