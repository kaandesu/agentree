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

// splitSystemPrompt instructs the brain to divide an APPROVED implementation
// plan into independent sub-tasks that can be built concurrently in separate
// git worktrees without stepping on each other. Each sub-task becomes its own
// claude agent, so its prompt must be fully self-contained.
const splitSystemPrompt = `You are the work-splitter for "agentree", an AI agent multiplexer.
You are given an APPROVED implementation plan. Divide it into INDEPENDENT
sub-tasks that can be built IN PARALLEL, each in its own isolated git worktree
by a separate Claude Code agent.

Rules:
- Split along seams that minimize file/merge conflicts (e.g. separate modules,
  packages, layers, or features). Prefer fewer, cleanly-separated sub-tasks
  over many overlapping ones.
- If the plan is small or so tightly coupled that splitting would cause
  conflicts, return a SINGLE sub-task containing the whole plan.
- Each sub-task's "prompt" MUST be self-contained: it should restate the
  relevant goal, the concrete work to do, and the parts of the plan it owns, so
  an agent that sees ONLY that prompt can build it. Tell the agent to explore
  the existing code and reuse patterns, and to stay within its slice of the work.

Respond with ONLY a compact JSON array, no prose:
[{"title": "<short title>", "prompt": "<self-contained build instructions>"}]`

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

// buildPromptFromPlan frames an entire plan as autonomous build instructions
// for a claude agent working inside an isolated worktree. Used for the
// no-split (single sub-task) fallback.
func buildPromptFromPlan(plan string) string {
	return fmt.Sprintf(`You are Claude Code working in an isolated git worktree. Implement the
following approved plan end to end. Explore the existing codebase first and
reuse its patterns and utilities. Work autonomously; make the changes, then
summarize what you did.

=== APPROVED PLAN ===
%s
=== END PLAN ===`, plan)
}

// wrapBuildPrompt frames a single split sub-task's instructions for an agent.
// The sub-task prompt is already self-contained; this adds the operating
// context (isolated worktree, autonomous, stay in your slice).
func wrapBuildPrompt(sub string) string {
	return fmt.Sprintf(`You are Claude Code working autonomously in an isolated git worktree on ONE
slice of a larger plan. Other agents are building the other slices in parallel,
so stay within your slice and avoid sweeping changes outside it. Explore the
existing codebase first and reuse its patterns. Implement the task below, then
summarize what you did.

=== YOUR TASK ===
%s
=== END TASK ===`, sub)
}
