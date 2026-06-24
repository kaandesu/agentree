package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"agentree/internal/brain"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestIdeaModalTriageAndFile drives the capture modal through compose →
// triage (stub brain) → review → file, asserting the stub's keyword heuristic
// sets priority and that the rationale is persisted.
func TestIdeaModalTriageAndFile(t *testing.T) {
	st := openTestStore(t)
	mod := newIdeaModal(st, brain.New(brain.OpenAI, "", ""), NewTheme()) // stub
	mod.ta.SetValue("urgent: login is broken\nusers cannot sign in")

	done, cmd := mod.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if done {
		t.Fatal("modal closed on compose submit; expected triage phase")
	}
	if mod.phase != modalTriaging {
		t.Fatalf("phase = %v, want triaging", mod.phase)
	}

	var triaged ideaTriagedMsg
	got := false
	for _, msg := range drain(cmd) {
		if tm, ok := msg.(ideaTriagedMsg); ok {
			triaged, got = tm, true
		}
	}
	if !got {
		t.Fatal("no ideaTriagedMsg produced")
	}
	mod.Update(triaged)
	if mod.phase != modalReview {
		t.Fatalf("phase = %v, want review", mod.phase)
	}
	if mod.priority != 1 {
		t.Errorf("priority = %d, want 1 (stub urgent heuristic)", mod.priority)
	}

	done, cmd = mod.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !done {
		t.Fatal("modal did not close on review file")
	}
	captured := false
	for _, msg := range drain(cmd) {
		if _, ok := msg.(ideaCapturedMsg); ok {
			captured = true
		}
	}
	if !captured {
		t.Fatal("no ideaCapturedMsg")
	}

	ideas, _ := st.ListIdeas(ctx())
	if len(ideas) != 1 {
		t.Fatalf("stored ideas = %d, want 1", len(ideas))
	}
	if ideas[0].Priority != 1 {
		t.Errorf("stored priority = %d, want 1", ideas[0].Priority)
	}
	if strings.TrimSpace(ideas[0].Rationale) == "" {
		t.Error("rationale not persisted")
	}
}

// TestIdeaModalOverridePriority confirms 1..5 in review overrides the brain's
// suggestion before filing.
func TestIdeaModalOverridePriority(t *testing.T) {
	st := openTestStore(t)
	mod := newIdeaModal(st, brain.New(brain.OpenAI, "", ""), NewTheme())
	mod.ta.SetValue("urgent thing")
	mod.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mod.Update(mod.triageCmd()()) // resolve triage directly
	if mod.priority != 1 {
		t.Fatalf("pre-override priority = %d, want 1", mod.priority)
	}
	mod.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if mod.priority != 4 {
		t.Errorf("override priority = %d, want 4", mod.priority)
	}
}

// TestIdeasInlineOverride confirms the Ideas tab rebinds an idea's priority via
// the 1..5 keys and re-reads from the store.
func TestIdeasInlineOverride(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.CreateIdea(ctx(), store.Idea{Title: "x", Priority: 3}); err != nil {
		t.Fatal(err)
	}
	id := newIdeas(st, NewTheme())
	for _, msg := range drain(id.Init()) {
		id.Update(msg)
	}
	_, cmd := id.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	for _, msg := range drain(cmd) {
		id.Update(msg)
	}
	ideas, _ := st.ListIdeas(ctx())
	if ideas[0].Priority != 1 {
		t.Errorf("priority after override = %d, want 1", ideas[0].Priority)
	}
	if ideas[0].Rationale != "manual override" {
		t.Errorf("rationale = %q, want manual override", ideas[0].Rationale)
	}
}

// TestIdeasPromoteEmitsMsg confirms pressing p on a selected idea emits a
// promoteIdeaMsg carrying that idea.
func TestIdeasPromoteEmitsMsg(t *testing.T) {
	st := openTestStore(t)
	st.CreateIdea(ctx(), store.Idea{Title: "promote me", Priority: 2})
	id := newIdeas(st, NewTheme())
	for _, msg := range drain(id.Init()) {
		id.Update(msg)
	}
	_, cmd := id.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	var pm *promoteIdeaMsg
	for _, msg := range drain(cmd) {
		if m, ok := msg.(promoteIdeaMsg); ok {
			pm = &m
		}
	}
	if pm == nil {
		t.Fatal("no promoteIdeaMsg")
	}
	if pm.idea.Title != "promote me" {
		t.Errorf("promoted idea = %q", pm.idea.Title)
	}
}

func TestRenderIdeasMarkdown(t *testing.T) {
	items := []store.Idea{
		{Title: "critical fix", Priority: 1, Rationale: "blocks release"},
		{Title: "nice to have", Priority: 5, Body: "someday"},
	}
	out := renderIdeasMarkdown(items)
	for _, want := range []string{"## P1", "critical fix", "blocks release", "## P5", "nice to have"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q; got:\n%s", want, out)
		}
	}
}
