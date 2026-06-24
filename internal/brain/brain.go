// Package brain is agentree's LLM "brain": the intelligent layer used for
// expanding raw specs into plan-mode prompts, triaging ideas into priorities,
// and (later) scheduling suggestions. It is provider-agnostic (OpenAI or
// Anthropic over HTTP) and degrades gracefully to a deterministic stub when no
// API key is configured, so the app is always usable.
package brain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Triage is the result of prioritizing an idea.
type Triage struct {
	Priority  int    `json:"priority"`
	Rationale string `json:"rationale"`
}

// SubTask is one independently-buildable unit produced by splitting an approved
// plan. Each carries a self-contained build prompt handed to a claude agent
// running in its own git worktree, so several can build in parallel.
type SubTask struct {
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
}

// Brain is the capability surface used by the rest of the app.
type Brain interface {
	// ExpandSpec turns a raw user spec into a large plan-mode prompt.
	ExpandSpec(ctx context.Context, raw string) (string, error)
	// TriageIdea assigns a priority 1..5 with a short rationale.
	TriageIdea(ctx context.Context, title, body string) (Triage, error)
	// SplitPlan divides an approved plan into independent, parallel-safe
	// sub-tasks. It always returns at least one sub-task (the whole plan) so
	// the caller can proceed even when no real LLM is configured.
	SplitPlan(ctx context.Context, plan string) ([]SubTask, error)
	// Suggest refines the supervisor's deterministic suggestion texts into
	// crisper, friendlier phrasings, preserving order and count. The stub
	// returns them unchanged, so suggestions work identically offline.
	Suggest(ctx context.Context, situations []string) ([]string, error)
	// Available reports whether a real LLM backs this brain.
	Available() bool
}

// Provider identifies the LLM backend.
type Provider string

const (
	OpenAI    Provider = "openai"
	Anthropic Provider = "anthropic"
)

// New returns a Brain for the given provider/model. If apiKey is empty it
// returns the deterministic stub so the app works without credentials.
func New(provider Provider, model, apiKey string) Brain {
	if strings.TrimSpace(apiKey) == "" {
		return stub{}
	}
	switch provider {
	case Anthropic:
		return &httpBrain{provider: Anthropic, model: model, apiKey: apiKey, client: defaultClient()}
	default:
		return &httpBrain{provider: OpenAI, model: model, apiKey: apiKey, client: defaultClient()}
	}
}

// ---- stub ----

type stub struct{}

func (stub) Available() bool { return false }

func (stub) ExpandSpec(_ context.Context, raw string) (string, error) {
	return wrapRawSpec(raw), nil
}

// SplitPlan (stub) does not divide work — without an LLM we can't reason about
// parallel-safety — so it returns the whole plan as a single build sub-task.
func (stub) SplitPlan(_ context.Context, plan string) ([]SubTask, error) {
	return []SubTask{singleSubTask(plan)}, nil
}

// Suggest (stub) passes the deterministic rule text through verbatim.
func (stub) Suggest(_ context.Context, situations []string) ([]string, error) {
	return situations, nil
}

func (stub) TriageIdea(_ context.Context, title, body string) (Triage, error) {
	// Heuristic: shout-ier / urgent-sounding ideas get a slightly higher
	// priority; everything else lands in the middle. Deterministic + offline.
	p := 3
	t := strings.ToLower(title + " " + body)
	switch {
	case containsAny(t, "urgent", "asap", "critical", "broken", "bug", "security"):
		p = 1
	case containsAny(t, "important", "soon", "should"):
		p = 2
	case containsAny(t, "someday", "maybe", "nice to have", "eventually"):
		p = 5
	}
	return Triage{Priority: p, Rationale: "heuristic (no LLM configured)"}, nil
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// singleSubTask wraps an entire plan as one build sub-task. Used as the
// no-split fallback by both the stub and the HTTP brain (on API/parse error).
func singleSubTask(plan string) SubTask {
	title := firstNonEmptyLine(plan)
	if title == "" {
		title = "Implement plan"
	}
	return SubTask{Title: title, Prompt: buildPromptFromPlan(plan)}
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#- *"))
		if line != "" {
			if len(line) > 80 {
				line = line[:80]
			}
			return line
		}
	}
	return ""
}

// parseSplit extracts a []SubTask from a model response that should be a JSON
// array, tolerating code fences / surrounding prose by scanning for the array.
// Empty/promptless entries are dropped; an empty result is an error so the
// caller falls back to a single sub-task.
func parseSplit(s string) ([]SubTask, error) {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return nil, errors.New("no JSON array in split response")
	}
	var raw []SubTask
	if err := json.Unmarshal([]byte(s[start:end+1]), &raw); err != nil {
		return nil, err
	}
	var out []SubTask
	for _, st := range raw {
		st.Title = strings.TrimSpace(st.Title)
		st.Prompt = strings.TrimSpace(st.Prompt)
		if st.Prompt == "" {
			continue
		}
		if st.Title == "" {
			st.Title = firstNonEmptyLine(st.Prompt)
		}
		out = append(out, st)
	}
	if len(out) == 0 {
		return nil, errors.New("split produced no usable sub-tasks")
	}
	return out, nil
}

// parseStringArray extracts a []string from a model response that should be a
// JSON array, tolerating code fences / surrounding prose.
func parseStringArray(s string) ([]string, error) {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return nil, errors.New("no JSON array in response")
	}
	var out []string
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// parseTriage extracts a Triage from a model response that should be JSON,
// tolerating code fences / surrounding prose by scanning for the JSON object.
func parseTriage(s string) (Triage, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}
	var t Triage
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return Triage{}, err
	}
	if t.Priority < 1 {
		t.Priority = 1
	}
	if t.Priority > 5 {
		t.Priority = 5
	}
	return t, nil
}
