package brain

import (
	"context"
	"strings"
	"testing"
)

func TestNewFallsBackToStubWithoutKey(t *testing.T) {
	b := New(OpenAI, "gpt-4o", "")
	if b.Available() {
		t.Fatal("expected stub (unavailable) when no API key")
	}
}

func TestStubExpandSpecWrapsAndInterrogates(t *testing.T) {
	b := New(OpenAI, "", "")
	out, err := b.ExpandSpec(context.Background(), "build a todo app with auth")
	if err != nil {
		t.Fatal(err)
	}
	// Must preserve the raw spec verbatim and instruct aggressive questioning.
	if !strings.Contains(out, "build a todo app with auth") {
		t.Error("expanded prompt dropped the raw spec")
	}
	for _, want := range []string{"PLAN MODE", "clarifying questions", "spec engineer"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded prompt missing %q", want)
		}
	}
}

func TestStubTriageHeuristics(t *testing.T) {
	b := New(OpenAI, "", "")
	ctx := context.Background()
	cases := []struct {
		title string
		want  int
	}{
		{"critical security bug in login", 1},
		{"important: refactor soon", 2},
		{"normal feature", 3},
		{"someday maybe add themes", 5},
	}
	for _, c := range cases {
		tr, err := b.TriageIdea(ctx, c.title, "")
		if err != nil {
			t.Fatal(err)
		}
		if tr.Priority != c.want {
			t.Errorf("TriageIdea(%q).Priority = %d, want %d", c.title, tr.Priority, c.want)
		}
	}
}

func TestParseTriageToleratesProse(t *testing.T) {
	tr, err := parseTriage("Sure! Here you go:\n```json\n{\"priority\": 2, \"rationale\": \"high impact\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Priority != 2 || tr.Rationale != "high impact" {
		t.Errorf("got %+v", tr)
	}
}
