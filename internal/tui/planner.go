package tui

import (
	"fmt"
	"strings"

	"agentree/internal/brain"
	"agentree/internal/store"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type plannerPhase int

const (
	phaseSelectProject plannerPhase = iota
	phaseChat
)

// planner drives the in-TUI AI chat for plan creation. The user selects a
// project, then converses with the AI (OpenAI) which interrogates first and
// eventually proposes a tree of features/sub-tasks via function calling. The
// user confirms the proposal to spawn parallel agents.
type planner struct {
	store *store.Store
	brain brain.Brain
	theme Theme
	w, h  int

	phase    plannerPhase
	projects []store.Project
	cursor   int
	selected *store.Project
	list     list.Model
	notice   string

	// pendingIdeaID links the next launched task back to a promoted idea.
	pendingIdeaID *int64

	// Chat state (phaseChat).
	messages []brain.ChatMessage  // conversation history (user + assistant turns)
	proposal *brain.PlanProposal  // current proposal (nil until AI calls propose_plan)
	input    textarea.Model       // chat input box
	vp       viewport.Model       // scrollable chat messages
	thinking bool                 // waiting for AI response
}

// --- messages emitted by the planner ---

// chatResponseMsg is an AI text reply (interrogation).
type chatResponseMsg struct{ text string }

// chatProposalMsg is the AI's structured plan proposal (function call).
type chatProposalMsg struct{ proposal brain.PlanProposal }

// chatErrorMsg surfaces an API error in the chat.
type chatErrorMsg struct{ err error }

// launchFromProposalMsg asks the root to spawn agents from a confirmed proposal.
type launchFromProposalMsg struct {
	project  store.Project
	proposal brain.PlanProposal
	spec     string // original user spec (first user message)
	ideaID   *int64
}

// planSessionStartedMsg notifies the planner that its request was launched.
type planSessionStartedMsg struct{ title string }

func newPlanner(st *store.Store, br brain.Brain, th Theme) *planner {
	input := textarea.New()
	input.Placeholder = "Describe the feature/product in as much detail as you want..."
	input.CharLimit = 0
	input.SetHeight(3)

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(colActive).BorderForeground(colActive)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(colSubtle).BorderForeground(colActive)
	l := list.New(nil, delegate, 0, 0)
	l.Title = "Projects"
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	vp := viewport.New(0, 0)

	return &planner{
		store: st, brain: br, theme: th,
		phase: phaseSelectProject,
		input: input, list: l, vp: vp,
	}
}

type projectListItem struct{ project store.Project }

func (p projectListItem) Title() string { return p.project.Name }
func (p projectListItem) Description() string {
	return fmt.Sprintf("%s  (%s)", p.project.RepoPath, p.project.DefaultBranch)
}
func (p projectListItem) FilterValue() string { return p.project.Name }

func (p *planner) Init() tea.Cmd { return p.refresh() }

// selectProject jumps straight to the chat phase for a given project (used
// when agentree is launched with a repo path argument).
func (p *planner) selectProject(pr store.Project) {
	p.selected = &pr
	p.phase = phaseChat
	p.initChat()
}

// prefill loads a promoted idea into the composer. If a project is already
// selected it jumps straight to chat; otherwise the draft is held until the
// user picks one. The idea id is carried onto the eventual task.
func (p *planner) prefill(idea store.Idea) {
	text := idea.Title
	if strings.TrimSpace(idea.Body) != "" {
		text += "\n\n" + idea.Body
	}
	p.input.SetValue(text)
	id := idea.ID
	p.pendingIdeaID = &id
	p.notice = "promoted idea — edit the spec, then ctrl+s to send"
	if p.selected != nil {
		p.phase = phaseChat
		p.initChat()
	} else {
		p.phase = phaseSelectProject
	}
}

func (p *planner) initChat() {
	p.messages = nil
	p.proposal = nil
	p.thinking = false
	p.input.Focus()
	p.updateViewport()
}

func (p *planner) refresh() tea.Cmd {
	return func() tea.Msg {
		items, err := p.store.ListProjects(ctx())
		if err != nil {
			return errMsg{err}
		}
		return projectsLoadedMsg{items}
	}
}

func (p *planner) Update(msg tea.Msg) (tab, tea.Cmd) {
	switch msg := msg.(type) {
	case projectsLoadedMsg:
		p.projects = msg.items
		if p.cursor >= len(p.projects) {
			p.cursor = 0
		}
		p.refreshProjectList()
		return p, nil
	case planSessionStartedMsg:
		p.notice = "launched agents for: " + msg.title
		p.resetChat()
		return p, nil
	case chatResponseMsg:
		p.thinking = false
		p.messages = append(p.messages, brain.ChatMessage{Role: "assistant", Content: msg.text})
		p.updateViewport()
		return p, nil
	case chatProposalMsg:
		p.thinking = false
		p.proposal = &msg.proposal
		// Add a synthetic assistant message summarizing the proposal.
		summary := fmt.Sprintf("I've prepared a plan with %d feature(s) and %d agent(s). Review the tree on the right, then press ctrl+enter to launch — or keep chatting to refine.",
			len(msg.proposal.Features), msg.proposal.TotalAgents())
		p.messages = append(p.messages, brain.ChatMessage{Role: "assistant", Content: summary})
		p.updateViewport()
		return p, nil
	case chatErrorMsg:
		p.thinking = false
		p.messages = append(p.messages, brain.ChatMessage{Role: "assistant", Content: "error: " + msg.err.Error()})
		p.updateViewport()
		return p, nil
	case tea.KeyMsg:
		if p.phase == phaseSelectProject {
			return p.updateSelect(msg)
		}
		return p.updateChat(msg)
	}
	return p, nil
}

func (p *planner) updateSelect(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(msg)
		p.cursor = p.list.Index()
		return p, cmd
	case "down", "j":
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(msg)
		p.cursor = p.list.Index()
		return p, cmd
	case "r":
		return p, p.refresh()
	case "enter":
		if len(p.projects) == 0 {
			p.notice = "no projects — register one in the Projects tab (press 3, then a)"
			return p, nil
		}
		item, ok := p.list.SelectedItem().(projectListItem)
		if !ok {
			return p, nil
		}
		sel := item.project
		p.selected = &sel
		p.phase = phaseChat
		p.notice = ""
		p.initChat()
		return p, textarea.Blink
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	p.cursor = p.list.Index()
	return p, cmd
}

func (p *planner) updateChat(msg tea.KeyMsg) (tab, tea.Cmd) {
	switch msg.String() {
	case "esc":
		p.phase = phaseSelectProject
		p.input.Blur()
		p.resetChat()
		return p, nil
	case "ctrl+s":
		// Send message to AI.
		text := strings.TrimSpace(p.input.Value())
		if text == "" || p.thinking || p.selected == nil {
			return p, nil
		}
		p.messages = append(p.messages, brain.ChatMessage{Role: "user", Content: text})
		p.input.Reset()
		p.thinking = true
		p.updateViewport()
		return p, p.planChatCmd()
	case "ctrl+enter":
		// Confirm proposal and launch agents.
		if p.proposal == nil || p.selected == nil {
			return p, nil
		}
		spec := p.firstUserMessage()
		proposal := *p.proposal
		project := *p.selected
		ideaID := p.pendingIdeaID
		return p, func() tea.Msg {
			return launchFromProposalMsg{
				project:  project,
				proposal: proposal,
				spec:     spec,
				ideaID:   ideaID,
			}
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

// planChatCmd sends the conversation to the AI off the Update loop.
func (p *planner) planChatCmd() tea.Cmd {
	br := p.brain
	msgs := make([]brain.ChatMessage, len(p.messages))
	copy(msgs, p.messages)
	return func() tea.Msg {
		resp, err := br.PlanChat(ctx(), msgs)
		if err != nil {
			return chatErrorMsg{err}
		}
		if resp.Proposal != nil {
			return chatProposalMsg{proposal: *resp.Proposal}
		}
		return chatResponseMsg{text: resp.Text}
	}
}

func (p *planner) firstUserMessage() string {
	for _, m := range p.messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
}

func (p *planner) resetChat() {
	p.messages = nil
	p.proposal = nil
	p.thinking = false
	p.input.Reset()
	p.selected = nil
	p.pendingIdeaID = nil
	p.phase = phaseSelectProject
}

func (p *planner) SetSize(w, h int) {
	p.w, p.h = w, h
	p.input.SetWidth(fitDim(w - 4))
	p.refreshProjectList()
	p.updateViewport()
}

func (p *planner) refreshProjectList() {
	items := make([]list.Item, 0, len(p.projects))
	for _, pr := range p.projects {
		items = append(items, projectListItem{project: pr})
	}
	_ = p.list.SetItems(items)
	p.list.SetSize(fitDim(p.w-4), fitDim(p.h-7))
	p.list.Select(p.cursor)
}

// updateViewport rebuilds the chat content and sizes the viewport.
func (p *planner) updateViewport() {
	chatW := p.chatWidth()
	inputH := 5 // textarea + help line + padding
	vpH := p.h - inputH - 3
	if vpH < 1 {
		vpH = 1
	}
	p.vp.Width = chatW
	p.vp.Height = vpH
	p.vp.SetContent(p.renderMessages(chatW - 2))
	p.vp.GotoBottom()
}

func (p *planner) chatWidth() int {
	if p.proposal != nil {
		// 60% for chat, 40% for tree panel.
		return fitDim((p.w - 4) * 60 / 100)
	}
	return fitDim(p.w - 4)
}

func (p *planner) treeWidth() int {
	return fitDim(p.w - 4 - p.chatWidth() - 1)
}

func (p *planner) View() string {
	var b strings.Builder

	if p.phase == phaseSelectProject {
		b.WriteString(p.theme.Subtle.Render("Select a project:"))
		b.WriteString("\n\n")
		if len(p.projects) == 0 {
			b.WriteString(p.theme.Subtle.Render("(none registered — go to Projects tab)"))
		} else {
			b.WriteString(p.list.View())
		}
		b.WriteString("\n")
		if p.notice != "" {
			b.WriteString(p.theme.Accent.Render(p.notice) + "\n")
		}
		b.WriteString(p.theme.Help.Render("↑/↓: move · enter: select · r: refresh"))
		return lipgloss.NewStyle().Width(p.w).Height(p.h).Padding(1, 2).Render(b.String())
	}

	// Chat phase.
	header := p.theme.Subtle.Render(fmt.Sprintf("Project: %s", p.selected.Name))

	// Chat column: viewport + input + help.
	chatContent := p.vp.View() + "\n" + p.input.View() + "\n" + p.chatHelp()
	chatCol := lipgloss.NewStyle().Width(p.chatWidth()).Render(header + "\n" + chatContent)

	if p.proposal == nil {
		return lipgloss.NewStyle().Width(p.w).Height(p.h).Padding(1, 2).Render(chatCol)
	}

	// Split view: chat left, tree right.
	treeCol := p.renderTree()
	body := lipgloss.JoinHorizontal(lipgloss.Top, chatCol, " ", treeCol)
	return lipgloss.NewStyle().Width(p.w).Height(p.h).Padding(1, 2).Render(body)
}

func (p *planner) chatHelp() string {
	help := "ctrl+s: send"
	if p.proposal != nil {
		help += " · ctrl+enter: confirm & launch"
	}
	help += " · esc: back"
	return p.theme.Help.Render(help)
}

// renderMessages formats the chat history for the viewport.
func (p *planner) renderMessages(width int) string {
	if len(p.messages) == 0 && !p.thinking {
		return p.theme.Subtle.Render("Type your spec and press ctrl+s to start the conversation...")
	}

	var lines []string
	userStyle := lipgloss.NewStyle().Foreground(colActive).Bold(true)
	aiStyle := lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	for _, m := range p.messages {
		var prefix string
		var style lipgloss.Style
		switch m.Role {
		case "user":
			prefix = "you"
			style = userStyle
		case "assistant":
			prefix = "ai"
			style = aiStyle
		default:
			continue
		}
		tag := style.Render("[" + prefix + "] ")
		// Word-wrap the content to fit the viewport width.
		contentW := width - lipgloss.Width(tag)
		if contentW < 20 {
			contentW = 20
		}
		wrapped := lipgloss.NewStyle().Width(contentW).Render(m.Content)
		// Indent continuation lines.
		msgLines := strings.Split(wrapped, "\n")
		for i, line := range msgLines {
			if i == 0 {
				lines = append(lines, tag+line)
			} else {
				lines = append(lines, strings.Repeat(" ", lipgloss.Width(tag))+line)
			}
		}
		lines = append(lines, "") // spacing between messages
	}

	if p.thinking {
		lines = append(lines, aiStyle.Render("[ai] ")+ p.theme.Subtle.Render("thinking..."))
	}

	return strings.Join(lines, "\n")
}

// renderTree draws the feature/sub-task tree panel.
func (p *planner) renderTree() string {
	if p.proposal == nil {
		return ""
	}
	tw := p.treeWidth()
	var b strings.Builder

	title := fmt.Sprintf("Features (%d features, %d agents)",
		len(p.proposal.Features), p.proposal.TotalAgents())
	b.WriteString(p.theme.Title.Render(title))
	b.WriteString("\n\n")

	treeGlyph := p.theme.Tree
	for i, f := range p.proposal.Features {
		featureTitle := fmt.Sprintf("%d. %s (%d agent", i+1, f.Title, len(f.SubTasks))
		if len(f.SubTasks) != 1 {
			featureTitle += "s"
		}
		featureTitle += ")"
		b.WriteString(p.theme.Accent.Bold(true).Render(featureTitle))
		b.WriteString("\n")
		for j, st := range f.SubTasks {
			connector := "├─"
			if j == len(f.SubTasks)-1 {
				connector = "└─"
			}
			line := treeGlyph.Render(connector) + " " + st.Title
			// Truncate if too wide.
			if lipgloss.Width(line) > tw-2 {
				line = line[:tw-5] + "..."
			}
			b.WriteString("   " + line + "\n")
		}
		if i < len(p.proposal.Features)-1 {
			b.WriteString("\n")
		}
	}

	vpH := p.h - 8
	if vpH < 1 {
		vpH = 1
	}
	return lipgloss.NewStyle().
		Width(tw).
		Height(vpH).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(1, 1).
		Render(b.String())
}

// CapturingInput implements tab; the planner always handles its own keys.
func (p *planner) CapturingInput() bool { return true }
