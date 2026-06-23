// Package brain is agentree's LLM "brain": the intelligent layer used for
// expanding raw specs into plan-mode prompts, triaging ideas into priorities,
// and (later) scheduling suggestions. It is provider-agnostic (OpenAI or
// Anthropic over HTTP) and degrades gracefully to a deterministic stub when no
// API key is configured, so the app is always usable.
package brain

import (
	"context"
	"encoding/json"
	"strings"
)

// Triage is the result of prioritizing an idea.
type Triage struct {
	Priority  int    `json:"priority"`
	Rationale string `json:"rationale"`
}

// Brain is the capability surface used by the rest of the app.
type Brain interface {
	// ExpandSpec turns a raw user spec into a large plan-mode prompt.
	ExpandSpec(ctx context.Context, raw string) (string, error)
	// TriageIdea assigns a priority 1..5 with a short rationale.
	TriageIdea(ctx context.Context, title, body string) (Triage, error)
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
