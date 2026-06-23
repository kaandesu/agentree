package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agentree/internal/config"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModelRenders verifies the root model wires up, processes a window size,
// drains the tabs' initial load commands, and renders the tab bar + content
// without panicking.
func TestModelRenders(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	if _, err := st.CreateProject(ctx, "demo", "/tmp/demo", "main"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := st.CreateIdea(ctx, store.Idea{Title: "ship it", Priority: 2}); err != nil {
		t.Fatalf("create idea: %v", err)
	}

	cfg := &config.Config{BrainProvider: config.ProviderOpenAI}
	var m tea.Model = New(cfg, st)

	// Initial commands (tab refreshes) — drain them synchronously.
	cmd := m.(Model).Init()
	for _, msg := range drain(cmd) {
		m, _ = m.Update(msg)
	}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	view := m.View()
	for _, want := range []string{"Dashboard", "Planner", "Ideas", "agentree"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q; got:\n%s", want, view)
		}
	}

	// Switch to Projects tab (key "3") and confirm the project shows.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if got := m.View(); !strings.Contains(got, "demo") {
		t.Errorf("projects view missing registered project; got:\n%s", got)
	}
}

// drain executes a tea.Cmd (and batched children) and returns the produced
// messages. It does not handle async tick commands, which is fine for the
// synchronous store-load commands used here.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch m := msg.(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, drain(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}
