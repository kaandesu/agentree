package tui

import (
	"fmt"
	"strconv"
	"strings"

	"agentree/internal/brain"
	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type modalPhase int

const (
	modalCompose  modalPhase = iota // typing the idea
	modalTriaging                   // brain.TriageIdea in flight
	modalReview                     // showing suggested priority/rationale
)

// ideaModal is the global idea-capture overlay (opened with ctrl+n). The user
// types an idea; on submit the brain triages it (priority 1..5 + rationale),
// the result is shown for review/override, then it's filed. Triage runs in a
// tea.Cmd so the Update loop never blocks; with the offline stub it resolves
// essentially instantly.
type ideaModal struct {
	store *store.Store
	brain brain.Brain
	theme Theme
	w, h  int

	phase modalPhase
	ta    textarea.Model
	sp    spinner.Model

	title     string
	body      string
	priority  int
	rationale string
}

// ideaCapturedMsg is emitted after an idea is filed so tabs can refresh.
type ideaCapturedMsg struct{ id int64 }

// ideaTriagedMsg carries the brain's triage result back into the modal.
type ideaTriagedMsg struct {
	priority  int
	rationale string
}

func newIdeaModal(st *store.Store, br brain.Brain, th Theme) ideaModal {
	ta := textarea.New()
	ta.Placeholder = "Pitch your idea… first line becomes the title."
	ta.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = th.Accent
	return ideaModal{store: st, brain: br, theme: th, ta: ta, sp: sp, phase: modalCompose, priority: 3}
}

func (m *ideaModal) Init() tea.Cmd { return textarea.Blink }

func (m *ideaModal) SetSize(w, h int) {
	m.w, m.h = w, h
	bw := min(w-8, 80)
	if bw < 20 {
		bw = 20
	}
	m.ta.SetWidth(bw - 4)
	m.ta.SetHeight(6)
}

// Update returns done=true when the modal should close. While open the root
// routes every message here (not just keys), so async triage + spinner ticks
// land correctly.
func (m *ideaModal) Update(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case ideaTriagedMsg:
		m.priority = clampPriority(msg.priority)
		m.rationale = msg.rationale
		m.phase = modalReview
		return false, nil
	case spinner.TickMsg:
		if m.phase != modalTriaging {
			return false, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		return false, cmd
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	if m.phase == modalCompose {
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return false, cmd
	}
	return false, nil
}

func (m *ideaModal) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch m.phase {
	case modalCompose:
		switch msg.String() {
		case "esc":
			return true, nil
		case "ctrl+s":
			text := strings.TrimSpace(m.ta.Value())
			if text == "" {
				return true, nil
			}
			m.title, m.body = splitTitleBody(text)
			m.phase = modalTriaging
			return false, tea.Batch(m.triageCmd(), m.sp.Tick)
		}
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return false, cmd
	case modalTriaging:
		if msg.String() == "esc" {
			return true, nil
		}
		return false, nil
	case modalReview:
		switch msg.String() {
		case "esc":
			return true, nil
		case "1", "2", "3", "4", "5":
			p, _ := strconv.Atoi(msg.String())
			m.priority = p
			// User overrode the brain — note it on the rationale.
			m.rationale = "manual override"
			return false, nil
		case "ctrl+s", "enter":
			return true, m.save()
		}
	}
	return false, nil
}

// triageCmd runs brain.TriageIdea off the Update loop. TriageIdea fails soft
// (returns a usable priority even on API error), so we always reach review.
func (m *ideaModal) triageCmd() tea.Cmd {
	br := m.brain
	title, body := m.title, m.body
	return func() tea.Msg {
		t, _ := br.TriageIdea(ctx(), title, body)
		return ideaTriagedMsg{priority: t.Priority, rationale: t.Rationale}
	}
}

func (m *ideaModal) save() tea.Cmd {
	title, body, priority, rationale := m.title, m.body, clampPriority(m.priority), m.rationale
	st := m.store
	return func() tea.Msg {
		idea, err := st.CreateIdea(ctx(), store.Idea{
			Title:     title,
			Body:      body,
			Priority:  priority,
			Rationale: rationale,
			Source:    "user",
		})
		if err != nil {
			return errMsg{err}
		}
		_ = st.AppendEvent(ctx(), "idea.added", `{"id":`+strconv.FormatInt(idea.ID, 10)+`}`)
		return ideaCapturedMsg{id: idea.ID}
	}
}

func (m *ideaModal) View() string {
	var body string
	switch m.phase {
	case modalCompose:
		body = m.theme.Title.Render("Capture idea") + "\n\n" +
			m.ta.View() + "\n\n" +
			m.theme.Help.Render("ctrl+s: triage & review  ·  esc: cancel")
	case modalTriaging:
		body = m.theme.Title.Render("Capture idea") + "\n\n" +
			m.theme.Subtle.Render(m.title) + "\n\n" +
			m.sp.View() + m.theme.Subtle.Render(" triaging…") + "\n\n" +
			m.theme.Help.Render("esc: cancel")
	case modalReview:
		body = m.theme.Title.Render("Review priority") + "\n\n" +
			m.theme.Accent.Render(m.title) + "\n\n" +
			fmt.Sprintf("Priority: %s\n", m.theme.Accent.Render("P"+strconv.Itoa(m.priority))) +
			m.theme.Subtle.Render(m.rationale) + "\n\n" +
			m.theme.Help.Render("1–5: override · ctrl+s/enter: file · esc: cancel")
	}
	box := m.theme.ModalBox.Render(body)
	return lipgloss.Place(m.w, m.h-2, lipgloss.Center, lipgloss.Center, box)
}

func splitTitleBody(text string) (title, body string) {
	parts := strings.SplitN(text, "\n", 2)
	title = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		body = strings.TrimSpace(parts[1])
	}
	if len(title) > 120 {
		title = title[:120]
	}
	return title, body
}

func clampPriority(p int) int {
	if p < 1 {
		return 1
	}
	if p > 5 {
		return 5
	}
	return p
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
