package brain

import "fmt"

// expandSystemPrompt instructs the brain to transform a user's raw, possibly
// rambling spec into a single, large, well-structured prompt to feed to
// `claude --permission-mode plan`. The defining goal: make claude interrogate
// the user aggressively, treating them as the spec engineer and the technical
// authority/advisor.
const expandSystemPrompt = `You are the planning front-end of "agentree", an AI agent multiplexer.
The user is a SPEC ENGINEER: they describe a product/feature in their own words.
Your job is to rewrite their raw input into ONE large, rigorous prompt that will
be handed verbatim to Claude Code running in PLAN MODE inside a git worktree.

The prompt you produce MUST instruct Claude to:
- Treat the user as the spec engineer who defines WHAT to build, and as the
  technical authority/advisor for HOW (technical choices) — ask the user when a
  technical decision matters, presenting trade-offs.
- Interrogate the user RELENTLESSLY: ask as many clarifying questions as
  possible, surface every ambiguity, edge case, and hole in the user's logic
  before proposing a plan. Prefer asking over assuming.
- Explore the existing codebase first, reuse existing patterns/utilities.
- Produce a concrete, staged implementation plan only after the questioning.

Preserve every concrete requirement from the user's input. Organize it into
clear sections (Goal, Features, Constraints, Open Questions, Technical choices
to confirm). Do NOT invent product decisions the user didn't state — turn
ambiguities into questions instead. Output ONLY the prompt text, no preamble.`

// triageSystemPrompt asks for a strict JSON triage of an idea.
const triageSystemPrompt = `You triage ideas for a developer's backlog into priority buckets 1..5,
where 1 is highest priority (do soon, high impact/urgency) and 5 is lowest
(someday/maybe). Consider impact, urgency, effort, and dependencies. Respond
with ONLY a compact JSON object: {"priority": <1-5>, "rationale": "<one sentence>"}.`

// wrapRawSpec is the deterministic fallback used when no LLM is configured. It
// wraps the user's raw spec with the same interrogation instructions so the
// app still works (and produces a usable plan-mode prompt) without an API key.
func wrapRawSpec(raw string) string {
	return fmt.Sprintf(`You are Claude Code in PLAN MODE. The user is a spec engineer.

Before proposing any plan, interrogate the user as thoroughly as possible: ask
many clarifying questions, surface every ambiguity, edge case, and gap in their
logic. Treat the user as the technical authority — when a technical choice
matters, present options with trade-offs and ask them to decide. Explore the
existing codebase and reuse existing patterns. Only after questioning, produce a
concrete, staged implementation plan.

=== USER SPEC (verbatim) ===
%s
=== END USER SPEC ===

Begin by asking your clarifying questions.`, raw)
}
