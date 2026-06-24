package tui

import (
	"context"
	"testing"

	"agentree/internal/brain"
	"agentree/internal/orchestrator"
	"agentree/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeShipper struct {
	pushed, prOpened, merged, discarded bool
	prURL                               string
	mergeErr                            error
}

func (f *fakeShipper) Push(context.Context, string, string) error { f.pushed = true; return nil }
func (f *fakeShipper) OpenPR(context.Context, string, string, string) (string, error) {
	f.prOpened = true
	return f.prURL, nil
}
func (f *fakeShipper) Merge(context.Context, string, string, string) error {
	f.merged = true
	return f.mergeErr
}
func (f *fakeShipper) Discard(context.Context, string, string, string) error {
	f.discarded = true
	return nil
}

func newShipTestModel(t *testing.T, fake *fakeShipper) (Model, int) {
	t.Helper()
	st := openTestStore(t)
	proj, _ := st.CreateProject(ctx(), "demo", "/tmp/demo", "main")
	task, _ := st.CreateTask(ctx(), store.Task{ProjectID: proj.ID, Title: "agent work", Status: store.StatusRunning})
	m := Model{
		store: st,
		brain: brain.New(brain.OpenAI, "", ""),
		tmux:  orchestrator.NewTmuxManager("agentree_shiptest"),
		ship:  fake,
		theme: NewTheme(),
		sessions: map[int]*session{
			7: {id: 7, taskID: task.ID, kind: "agent", windowID: "@9",
				dir: "/tmp/demo/wt", branch: "agentree/x", repoPath: "/tmp/demo", baseBranch: "main"},
		},
	}
	return m, 7
}

func TestShipPRFlow(t *testing.T) {
	fake := &fakeShipper{prURL: "https://github.com/x/y/pull/1"}
	m, sid := newShipTestModel(t, fake)

	msg := m.shipActionCmd(sid, "pr")()
	done, ok := msg.(shipDoneMsg)
	if !ok {
		t.Fatalf("expected shipDoneMsg, got %T", msg)
	}
	if !fake.pushed || !fake.prOpened {
		t.Errorf("PR flow did not push+open (pushed=%v pr=%v)", fake.pushed, fake.prOpened)
	}
	if done.url != fake.prURL {
		t.Errorf("url = %q, want %q", done.url, fake.prURL)
	}
}

func TestShipMergeMarksTaskDone(t *testing.T) {
	fake := &fakeShipper{}
	m, sid := newShipTestModel(t, fake)
	taskID := m.sessions[sid].taskID

	msg := m.shipActionCmd(sid, "merge")().(shipDoneMsg)
	if !fake.merged {
		t.Fatal("merge not invoked")
	}
	(&m).handleShipDone(msg)
	got, _ := m.store.GetTask(ctx(), taskID)
	if got.Status != store.StatusDone {
		t.Errorf("task status = %q, want done", got.Status)
	}
}

func TestShipMergeKeepsTaskOpenWhenSiblingsRemain(t *testing.T) {
	fake := &fakeShipper{}
	m, sid := newShipTestModel(t, fake)
	taskID := m.sessions[sid].taskID
	// A sibling agent for the same plan task is still around.
	m.sessions[8] = &session{id: 8, taskID: taskID, kind: "agent", windowID: "@10",
		dir: "/tmp/demo/wt2", branch: "agentree/y", repoPath: "/tmp/demo", baseBranch: "main"}

	msg := m.shipActionCmd(sid, "merge")().(shipDoneMsg)
	(&m).handleShipDone(msg)
	got, _ := m.store.GetTask(ctx(), taskID)
	if got.Status == store.StatusDone {
		t.Errorf("task marked done while a sibling agent remains; status=%q", got.Status)
	}
}

func TestShipDiscardRemovesSession(t *testing.T) {
	fake := &fakeShipper{}
	m, sid := newShipTestModel(t, fake)

	msg := m.shipActionCmd(sid, "discard")().(shipDoneMsg)
	if !fake.discarded {
		t.Fatal("discard not invoked")
	}
	(&m).handleShipDone(msg)
	if _, ok := m.sessions[sid]; ok {
		t.Errorf("session %d should be removed after discard", sid)
	}
}

func TestShipRequestOpensConfirm(t *testing.T) {
	fake := &fakeShipper{}
	m, sid := newShipTestModel(t, fake)
	(&m).handleShipRequest(sid, "merge")
	if m.overlay == nil {
		t.Fatal("expected a confirm overlay to open")
	}
	// Confirming runs the carried action; cancelling does nothing.
	done, cmd := m.overlay.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !done {
		t.Fatal("confirm did not close on enter")
	}
	if cmd == nil {
		t.Fatal("confirm produced no action command")
	}
	if _, ok := cmd().(shipDoneMsg); !ok {
		t.Errorf("confirmed action should yield shipDoneMsg")
	}
	if !fake.merged {
		t.Errorf("merge should have run after confirm")
	}
}
