package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"agentree/internal/orchestrator"

	tea "github.com/charmbracelet/bubbletea"
)

// suggestion is one proactive note the supervisor surfaces on the Dashboard.
// id dedups across ticks; hint is the action key/affordance.
type suggestion struct {
	id   string
	text string
	hint string
}

// suggestionsMsg carries the supervisor's current suggestions to the dashboard.
type suggestionsMsg struct{ suggestions []suggestion }

// idleThreshold is how long without tmux activity before a session is flagged.
const idleThreshold = 10 * time.Minute

// agentSnapshot is a race-free copy of one session's state, taken on the Update
// loop and handed to the (off-loop) detector + git calls.
type agentSnapshot struct {
	sessionID    int
	winID        string // tmux pane/window id, used to match live status by id
	title        string
	kind         string
	dead         bool
	exitCode     int
	worktree     string
	base         string
	lastActivity time.Time
	changed      map[string]bool
}

// supervisorInput is the full snapshot the pure detector reasons over.
type supervisorInput struct {
	now           time.Time
	idleThreshold time.Duration
	agents        []agentSnapshot
}

// detectSuggestions applies the deterministic rules. It is pure and fully
// testable: no I/O, no clock except the injected `now`.
func detectSuggestions(in supervisorInput) []suggestion {
	var out []suggestion
	seen := map[string]bool{}
	add := func(s suggestion) {
		if seen[s.id] {
			return
		}
		seen[s.id] = true
		out = append(out, s)
	}

	// Rule 1: errored exit.
	for _, a := range in.agents {
		if a.dead && a.exitCode != 0 {
			add(suggestion{
				id:   fmt.Sprintf("err:%d", a.sessionID),
				text: fmt.Sprintf("agent %q exited with an error (code %d) — restart it?", a.title, a.exitCode),
				hint: "ctrl+o to inspect",
			})
		}
	}

	// Rule 2: file overlap between any two agents (possible conflict).
	for i := 0; i < len(in.agents); i++ {
		for j := i + 1; j < len(in.agents); j++ {
			a, b := in.agents[i], in.agents[j]
			shared := intersect(a.changed, b.changed)
			if len(shared) == 0 {
				continue
			}
			lo, hi := a.sessionID, b.sessionID
			if lo > hi {
				lo, hi = hi, lo
			}
			add(suggestion{
				id:   fmt.Sprintf("conflict:%d-%d", lo, hi),
				text: fmt.Sprintf("agents %q and %q both touched %s — possible conflict", a.title, b.title, summarizePaths(shared)),
				hint: "review diffs (d)",
			})
		}
	}

	// Rule 3: idle (live sessions only — a dead window's activity is frozen).
	for _, a := range in.agents {
		if a.dead || a.lastActivity.IsZero() {
			continue
		}
		if in.now.Sub(a.lastActivity) > in.idleThreshold {
			add(suggestion{
				id:   fmt.Sprintf("idle:%d", a.sessionID),
				text: fmt.Sprintf("%q has been idle %s — stuck?", a.title, formatDuration(in.now.Sub(a.lastActivity))),
				hint: "ctrl+o to check",
			})
		}
	}
	return out
}

func intersect(a, b map[string]bool) []string {
	var out []string
	for p := range a {
		if b[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func summarizePaths(paths []string) string {
	if len(paths) == 1 {
		return paths[0]
	}
	return fmt.Sprintf("%s (+%d more)", paths[0], len(paths)-1)
}

// superviseCmd snapshots session state on the Update loop, then (off-loop)
// computes each agent's changed-file set, runs the detector, and — when a real
// brain is available — lets it rephrase the suggestion text. The stub passes
// text through verbatim, so behavior is identical offline.
func (m *Model) superviseCmd() tea.Cmd {
	var snaps []agentSnapshot
	for _, s := range m.sessions {
		if s.kind != "agent" {
			continue
		}
		snaps = append(snaps, agentSnapshot{
			sessionID: s.id,
			winID:     s.windowID,
			title:     s.title,
			kind:      s.kind,
			dead:      s.dead,
			worktree:  s.dir,
			base:      s.baseBranch,
		})
	}
	if len(snaps) == 0 {
		return func() tea.Msg { return suggestionsMsg{} }
	}
	// Carry the live window activity/exit info captured this tick.
	br := m.brain
	tmux := m.tmux
	return func() tea.Msg {
		c := ctx()
		// Refresh per-agent exit code + activity + changed set off the loop.
		// Match by tmux id (robust: claude can overwrite a pane's title).
		wins, _ := tmux.ListWindows(c)
		byID := map[string]orchestrator.Window{}
		for _, w := range wins {
			byID[w.ID] = w
		}
		for i := range snaps {
			if snaps[i].worktree != "" {
				if paths, err := orchestrator.ChangedPaths(c, snaps[i].worktree, snaps[i].base); err == nil {
					snaps[i].changed = paths
				}
			}
			if w, ok := byID[snaps[i].winID]; ok {
				snaps[i].dead = w.Dead
				snaps[i].exitCode = w.Status
				snaps[i].lastActivity = w.LastActivity
			}
		}
		sugs := detectSuggestions(supervisorInput{
			now: time.Now(), idleThreshold: idleThreshold, agents: snaps,
		})
		if br.Available() && len(sugs) > 0 {
			texts := make([]string, len(sugs))
			for i, s := range sugs {
				texts[i] = s.text
			}
			if phrased, err := br.Suggest(c, texts); err == nil && len(phrased) == len(sugs) {
				for i := range sugs {
					if strings.TrimSpace(phrased[i]) != "" {
						sugs[i].text = phrased[i]
					}
				}
			}
		}
		return suggestionsMsg{suggestions: sugs}
	}
}
