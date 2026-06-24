// Package store is the SQLite persistence layer for agentree. All structured
// state (projects, ideas, tasks, runs, events, settings) lives here. Ideas'
// "priority folders" are a virtual view derived from the priority column.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go sqlite driver (no cgo)
)

// Store wraps the SQLite connection and exposes typed queries.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the
// schema. WAL is enabled for better concurrent read behavior under the TUI.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Single writer avoids "database is locked" under the single-threaded
	// update loop plus background goroutines.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := applyMigrations(db); err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for advanced queries / tests.
func (s *Store) DB() *sql.DB { return s.db }

// ---- Projects ----

func (s *Store) CreateProject(ctx context.Context, name, repoPath, branch string) (*Project, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO projects (name, repo_path, default_branch) VALUES (?, ?, ?)`,
		name, repoPath, branch)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetProject(ctx, id)
}

// GetProjectByPath looks up a project by its repo path. Returns (nil, nil) if
// not found.
func (s *Store) GetProjectByPath(ctx context.Context, repoPath string) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, repo_path, default_branch, created_at FROM projects WHERE repo_path = ?`, repoPath).
		Scan(&p.ID, &p.Name, &p.RepoPath, &p.DefaultBranch, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// GetOrCreateProject returns the existing project for repoPath or creates one.
func (s *Store) GetOrCreateProject(ctx context.Context, name, repoPath, branch string) (*Project, error) {
	if existing, err := s.GetProjectByPath(ctx, repoPath); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	return s.CreateProject(ctx, name, repoPath, branch)
}

func (s *Store) GetProject(ctx context.Context, id int64) (*Project, error) {
	p := &Project{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, repo_path, default_branch, created_at FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.RepoPath, &p.DefaultBranch, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, repo_path, default_branch, created_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoPath, &p.DefaultBranch, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- Ideas ----

func (s *Store) CreateIdea(ctx context.Context, i Idea) (*Idea, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO ideas (project_id, title, body, priority, rationale, status, source) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		i.ProjectID, i.Title, i.Body, i.Priority, i.Rationale, nz(i.Status, "open"), nz(i.Source, "user"))
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	i.ID = id
	return &i, nil
}

func (s *Store) ListIdeas(ctx context.Context) ([]Idea, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, title, body, priority, rationale, status, source, created_at
		 FROM ideas WHERE status != 'archived' ORDER BY priority, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Idea
	for rows.Next() {
		var i Idea
		if err := rows.Scan(&i.ID, &i.ProjectID, &i.Title, &i.Body, &i.Priority, &i.Rationale, &i.Status, &i.Source, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) SetIdeaPriority(ctx context.Context, id int64, priority int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ideas SET priority = ? WHERE id = ?`, priority, id)
	return err
}

// UpdateIdeaTriage records both the priority and the brain's rationale, used
// after the capture modal triages an idea and when the user overrides inline.
func (s *Store) UpdateIdeaTriage(ctx context.Context, id int64, priority int, rationale string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE ideas SET priority = ?, rationale = ? WHERE id = ?`, priority, rationale, id)
	return err
}

// SetIdeaStatus updates an idea's lifecycle status (e.g. "promoted", "archived").
func (s *Store) SetIdeaStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ideas SET status = ? WHERE id = ?`, status, id)
	return err
}

// ---- Tasks ----

func (s *Store) CreateTask(ctx context.Context, t Task) (*Task, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tasks (project_id, idea_id, title, spec, expanded_prompt, status, agent_kind, worktree_path, branch)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.IdeaID, t.Title, t.Spec, t.ExpandedPrompt,
		nzStatus(t.Status, StatusQueued), nzKind(t.AgentKind, AgentClaude), t.WorktreePath, t.Branch)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTask(ctx, id)
}

func (s *Store) GetTask(ctx context.Context, id int64) (*Task, error) {
	t := &Task{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, project_id, idea_id, title, spec, expanded_prompt, status, agent_kind, worktree_path, branch, plan_path, created_at, updated_at
		 FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.ProjectID, &t.IdeaID, &t.Title, &t.Spec, &t.ExpandedPrompt,
			&t.Status, &t.AgentKind, &t.WorktreePath, &t.Branch, &t.PlanPath, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) ListTasks(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, idea_id, title, spec, expanded_prompt, status, agent_kind, worktree_path, branch, plan_path, created_at, updated_at
		 FROM tasks ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.IdeaID, &t.Title, &t.Spec, &t.ExpandedPrompt,
			&t.Status, &t.AgentKind, &t.WorktreePath, &t.Branch, &t.PlanPath, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UpdateTaskStatus(ctx context.Context, id int64, status TaskStatus) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	return err
}

// SetTaskPlanReady records the ingested plan path and moves the task to ready.
func (s *Store) SetTaskPlanReady(ctx context.Context, id int64, planPath string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET plan_path = ?, status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		planPath, StatusReady, id)
	return err
}

func (s *Store) UpdateTaskWorktree(ctx context.Context, id int64, path, branch string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET worktree_path = ?, branch = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		path, branch, id)
	return err
}

// ---- Events ----

func (s *Store) AppendEvent(ctx context.Context, typ, payload string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events (type, payload) VALUES (?, ?)`, typ, nz(payload, "{}"))
	return err
}

// ---- helpers ----

func nz(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nzStatus(s, def TaskStatus) TaskStatus {
	if s == "" {
		return def
	}
	return s
}

func nzKind(s, def AgentKind) AgentKind {
	if s == "" {
		return def
	}
	return s
}
