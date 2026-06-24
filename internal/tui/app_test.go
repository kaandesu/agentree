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
// drains the tabs' initial load commands, and renders the sidebar + content
// without panicking.
func TestModelRenders(t *testing.T) {
	m, _ := newRenderedTestModel(t, 100, 30)

	view := m.View()
	for _, want := range []string{"Dashboard", "Planner", "Projects", "Ideas", "Tasks", "agentree", "ctrl+n", "tab"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q; got:\n%s", want, view)
		}
	}

	// Switch to Projects tab (key "3") and confirm the table shows the project.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if got := m.View(); !strings.Contains(got, "demo") || !strings.Contains(got, "Branch") {
		t.Errorf("projects view missing registered project table; got:\n%s", got)
	}
}

func TestModelCompactChromeRenders(t *testing.T) {
	m, _ := newRenderedTestModel(t, 60, 18)

	view := m.View()
	for _, want := range []string{"1 Dashboard", "2 Planner", "agentree"} {
		if !strings.Contains(view, want) {
			t.Errorf("compact view missing %q; got:\n%s", want, view)
		}
	}
}

func TestNumericShortcutsDoNotStealCapturedInput(t *testing.T) {
	tm, _ := newRenderedTestModel(t, 100, 30)
	m := tm.(Model)

	// Switch to Projects and enter add mode; that tab now captures text input.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = updated.(Model)

	if m.active != tabProjects {
		t.Fatalf("numeric input switched tabs while Projects was capturing input; active=%v", m.active)
	}
	if !strings.Contains(m.View(), "repo path") {
		t.Fatalf("projects add input lost focus; got:\n%s", m.View())
	}
}

func newRenderedTestModel(t *testing.T, width, height int) (tea.Model, *store.Store) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	proj, err := st.CreateProject(ctx, "demo", "/tmp/demo", "main")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := st.CreateIdea(ctx, store.Idea{Title: "ship it", Priority: 2}); err != nil {
		t.Fatalf("create idea: %v", err)
	}
	if _, err := st.CreateTask(ctx, store.Task{ProjectID: proj.ID, Title: "ship dashboard", Status: store.StatusRunning, AgentKind: store.AgentCodex}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	cfg := &config.Config{BrainProvider: config.ProviderOpenAI}
	var m tea.Model = New(cfg, st)

	// Initial commands (tab refreshes) — drain them synchronously.
	cmd := m.(Model).Init()
	for _, msg := range drain(cmd) {
		m, _ = m.Update(msg)
	}

	m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m, st
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
