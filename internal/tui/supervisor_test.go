package tui

import (
	"strings"
	"testing"
	"time"
)

func hasSuggestion(sugs []suggestion, idPrefix string) *suggestion {
	for i := range sugs {
		if strings.HasPrefix(sugs[i].id, idPrefix) {
			return &sugs[i]
		}
	}
	return nil
}

func TestDetectErroredExit(t *testing.T) {
	in := supervisorInput{
		now:           time.Now(),
		idleThreshold: idleThreshold,
		agents: []agentSnapshot{
			{sessionID: 1, title: "auth", dead: true, exitCode: 2},
			{sessionID: 2, title: "ui", dead: true, exitCode: 0}, // clean — no suggestion
		},
	}
	sugs := detectSuggestions(in)
	if hasSuggestion(sugs, "err:1") == nil {
		t.Errorf("expected errored-exit suggestion for agent 1; got %+v", sugs)
	}
	if hasSuggestion(sugs, "err:2") != nil {
		t.Errorf("clean exit should not produce an error suggestion")
	}
}

func TestDetectFileOverlap(t *testing.T) {
	in := supervisorInput{
		now:           time.Now(),
		idleThreshold: idleThreshold,
		agents: []agentSnapshot{
			{sessionID: 1, title: "a", changed: map[string]bool{"go.mod": true, "a.go": true}},
			{sessionID: 2, title: "b", changed: map[string]bool{"go.mod": true, "b.go": true}},
			{sessionID: 3, title: "c", changed: map[string]bool{"c.go": true}},
		},
	}
	sugs := detectSuggestions(in)
	conflict := hasSuggestion(sugs, "conflict:1-2")
	if conflict == nil {
		t.Fatalf("expected conflict suggestion for agents 1+2; got %+v", sugs)
	}
	if !strings.Contains(conflict.text, "go.mod") {
		t.Errorf("conflict text should name the shared file; got %q", conflict.text)
	}
	if hasSuggestion(sugs, "conflict:1-3") != nil || hasSuggestion(sugs, "conflict:2-3") != nil {
		t.Errorf("agent 3 shares no files; should not conflict")
	}
}

func TestDetectIdle(t *testing.T) {
	now := time.Now()
	in := supervisorInput{
		now:           now,
		idleThreshold: 10 * time.Minute,
		agents: []agentSnapshot{
			{sessionID: 1, title: "slow", lastActivity: now.Add(-11 * time.Minute)},
			{sessionID: 2, title: "busy", lastActivity: now.Add(-1 * time.Minute)},
			{sessionID: 3, title: "dead-old", dead: true, lastActivity: now.Add(-20 * time.Minute)}, // dead: frozen, skip
		},
	}
	sugs := detectSuggestions(in)
	if hasSuggestion(sugs, "idle:1") == nil {
		t.Errorf("expected idle suggestion for agent 1; got %+v", sugs)
	}
	if hasSuggestion(sugs, "idle:2") != nil {
		t.Errorf("recently active agent should not be flagged idle")
	}
	if hasSuggestion(sugs, "idle:3") != nil {
		t.Errorf("dead agent's frozen activity should not be flagged idle")
	}
}

func TestDetectNoFalsePositives(t *testing.T) {
	now := time.Now()
	in := supervisorInput{
		now:           now,
		idleThreshold: idleThreshold,
		agents: []agentSnapshot{
			{sessionID: 1, title: "a", changed: map[string]bool{"a.go": true}, lastActivity: now},
			{sessionID: 2, title: "b", changed: map[string]bool{"b.go": true}, lastActivity: now},
		},
	}
	if sugs := detectSuggestions(in); len(sugs) != 0 {
		t.Errorf("healthy agents produced suggestions: %+v", sugs)
	}
}
