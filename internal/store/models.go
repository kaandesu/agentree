package store

import "time"

// TaskStatus is the lifecycle state of a task.
type TaskStatus string

const (
	StatusQueued    TaskStatus = "queued"
	StatusPlanning  TaskStatus = "planning"
	StatusReady     TaskStatus = "ready"
	StatusRunning   TaskStatus = "running"
	StatusReview    TaskStatus = "review"
	StatusDone      TaskStatus = "done"
	StatusDiscarded TaskStatus = "discarded"
)

// AgentKind identifies which external CLI runs a task.
type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

// Project is a registered git repository agentree can manage.
type Project struct {
	ID            int64
	Name          string
	RepoPath      string
	DefaultBranch string
	CreatedAt     time.Time
}

// Idea is a captured thought, auto-triaged into a priority 1..5 bucket.
type Idea struct {
	ID        int64
	ProjectID *int64 // nullable: ideas may be global
	Title     string
	Body      string
	Priority  int // 1 (highest) .. 5 (lowest)
	Status    string
	Source    string
	CreatedAt time.Time
}

// Task is a unit of work that becomes a worktree + agent.
type Task struct {
	ID             int64
	ProjectID      int64
	IdeaID         *int64
	Title          string
	Spec           string // raw user spec
	ExpandedPrompt string // brain-expanded plan-mode prompt
	Status         TaskStatus
	AgentKind      AgentKind
	WorktreePath   string
	Branch         string
	PlanPath       string // path to the ingested plan file (best-effort)
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Run records a single agent invocation for a task.
type Run struct {
	ID        int64
	TaskID    int64
	Kind      AgentKind
	PID       int
	Status    string
	StartedAt time.Time
	EndedAt   *time.Time
	ExitCode  *int
}

// Event feeds the event-driven supervisor.
type Event struct {
	ID        int64
	Type      string
	Payload   string // JSON
	CreatedAt time.Time
}
