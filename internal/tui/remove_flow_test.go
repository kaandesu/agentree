package tui

import (
	"testing"

	"agentree/internal/brain"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

// hasEvent reports whether an event of the given type was recorded.
func hasEvent(t *testing.T, st *store.Store, typ string) bool {
	t.Helper()
	evs, err := st.RecentEvents(ctx(), 50)
	if err != nil {
		t.Fatalf("recent events: %v", err)
	}
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// TestIdeasDeleteEmitsRequest confirms pressing x on a selected idea emits a
// deleteIdeaRequestMsg carrying that idea.
func TestIdeasDeleteEmitsRequest(t *testing.T) {
	st := openTestStore(t)
	st.CreateIdea(ctx(), store.Idea{Title: "delete me", Priority: 2})
	id := newIdeas(st, NewTheme())
	for _, msg := range drain(id.Init()) {
		id.Update(msg)
	}
	_, cmd := id.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	var dm *deleteIdeaRequestMsg
	for _, msg := range drain(cmd) {
		if m, ok := msg.(deleteIdeaRequestMsg); ok {
			dm = &m
		}
	}
	if dm == nil {
		t.Fatal("no deleteIdeaRequestMsg")
	}
	if dm.idea.Title != "delete me" {
		t.Errorf("delete idea = %q", dm.idea.Title)
	}
}

// TestDeleteIdeaConfirmRemovesRow drives the full confirm flow: open the modal,
// press enter, and assert the idea row is gone and the event recorded.
func TestDeleteIdeaConfirmRemovesRow(t *testing.T) {
	st := openTestStore(t)
	idea, _ := st.CreateIdea(ctx(), store.Idea{Title: "gone soon", Priority: 3})
	m := Model{store: st, brain: brain.New(brain.OpenAI, "", ""), theme: NewTheme()}

	(&m).handleDeleteIdeaRequest(*idea)
	if m.overlay == nil {
		t.Fatal("expected a confirm overlay to open")
	}
	done, cmd := m.overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !done || cmd == nil {
		t.Fatalf("confirm did not close/produce a command (done=%v cmd=%v)", done, cmd)
	}
	for _, msg := range drain(cmd) {
		if e, ok := msg.(errMsg); ok {
			t.Fatalf("delete produced error: %v", e.err)
		}
	}
	ideas, _ := st.ListIdeas(ctx())
	if len(ideas) != 0 {
		t.Errorf("ideas after delete = %d, want 0", len(ideas))
	}
	if !hasEvent(t, st, store.EventIdeaDeleted) {
		t.Error("idea.deleted event not recorded")
	}
}

// TestDeleteIdeaCancelKeepsRow confirms esc/n cancels without deleting.
func TestDeleteIdeaCancelKeepsRow(t *testing.T) {
	st := openTestStore(t)
	idea, _ := st.CreateIdea(ctx(), store.Idea{Title: "survivor", Priority: 3})
	m := Model{store: st, brain: brain.New(brain.OpenAI, "", ""), theme: NewTheme()}

	(&m).handleDeleteIdeaRequest(*idea)
	done, cmd := m.overlay.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !done {
		t.Fatal("confirm did not close on esc")
	}
	if cmd != nil {
		t.Fatal("cancel should not carry an action command")
	}
	if ideas, _ := st.ListIdeas(ctx()); len(ideas) != 1 {
		t.Errorf("ideas after cancel = %d, want 1", len(ideas))
	}
}

// TestRemoveQueuedTask removes a task with no live agents: it just flips to
// discarded and records the event.
func TestRemoveQueuedTask(t *testing.T) {
	fake := &fakeShipper{}
	m, sid := newShipTestModel(t, fake)
	taskID := m.sessions[sid].taskID
	// Queued task: no sessions for it.
	queued, _ := m.store.CreateTask(ctx(), store.Task{ProjectID: 1, Title: "queued", Status: store.StatusQueued})

	msg := m.removeTaskCmd(queued.ID, nil, nil)().(taskRemovedMsg)
	(&m).handleTaskRemoved(msg)

	got, _ := m.store.GetTask(ctx(), queued.ID)
	if got.Status != store.StatusDiscarded {
		t.Errorf("queued task status = %q, want discarded", got.Status)
	}
	if !hasEvent(t, m.store, store.EventTaskRemoved) {
		t.Error("task.removed event not recorded")
	}
	// The other running task is untouched.
	if _, ok := m.sessions[sid]; !ok {
		t.Errorf("unrelated session %d should remain", sid)
	}
	_ = taskID
}

// TestRemoveRunningTaskDropsSessionKeepsWorktree confirms removing a running
// task drops its agent session, marks the task discarded, and does NOT discard
// the worktree (kill-only).
func TestRemoveRunningTaskDropsSessionKeepsWorktree(t *testing.T) {
	fake := &fakeShipper{}
	m, sid := newShipTestModel(t, fake)
	taskID := m.sessions[sid].taskID

	(&m).handleRemoveTaskRequest(store.Task{ID: taskID, Title: "agent work"})
	if m.overlay == nil {
		t.Fatal("expected a confirm overlay to open")
	}
	done, cmd := m.overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !done || cmd == nil {
		t.Fatalf("confirm did not close/produce a command (done=%v cmd=%v)", done, cmd)
	}
	msg, ok := cmd().(taskRemovedMsg)
	if !ok {
		t.Fatalf("confirmed action should yield taskRemovedMsg")
	}
	(&m).handleTaskRemoved(msg)

	if _, ok := m.sessions[sid]; ok {
		t.Errorf("session %d should be dropped after removal", sid)
	}
	got, _ := m.store.GetTask(ctx(), taskID)
	if got.Status != store.StatusDiscarded {
		t.Errorf("task status = %q, want discarded", got.Status)
	}
	if fake.discarded {
		t.Error("worktree should be kept on removal (Discard must not run)")
	}
}
