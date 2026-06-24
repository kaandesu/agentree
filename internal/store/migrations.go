package store

import (
	"database/sql"
	"strings"
)

// additiveMigrations are idempotent ALTER statements that bring older databases
// (created before a column existed) up to the current schema. CREATE TABLE IF
// NOT EXISTS never alters an existing table, so new columns land here too.
// Re-adding an existing column errors with "duplicate column"; we ignore that.
var additiveMigrations = []string{
	`ALTER TABLE ideas ADD COLUMN rationale TEXT NOT NULL DEFAULT ''`,
}

func applyMigrations(db *sql.DB) error {
	for _, stmt := range additiveMigrations {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}

// schema is the full DDL applied idempotently at startup. New columns/tables
// should be added here with IF NOT EXISTS semantics or via numbered migrations
// once we ship; for now a single idempotent schema is sufficient.
const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL,
    repo_path      TEXT NOT NULL UNIQUE,
    default_branch TEXT NOT NULL DEFAULT 'main',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ideas (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER REFERENCES projects(id) ON DELETE SET NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    priority   INTEGER NOT NULL DEFAULT 3 CHECK (priority BETWEEN 1 AND 5),
    rationale  TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'open',
    source     TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tasks (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id      INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    idea_id         INTEGER REFERENCES ideas(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    spec            TEXT NOT NULL DEFAULT '',
    expanded_prompt TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'queued',
    agent_kind      TEXT NOT NULL DEFAULT 'claude',
    worktree_path   TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    plan_path       TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS runs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id    INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    pid        INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL DEFAULT 'running',
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at   DATETIME,
    exit_code  INTEGER
);

CREATE TABLE IF NOT EXISTS events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    type       TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status  ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_ideas_priority ON ideas(priority);
CREATE INDEX IF NOT EXISTS idx_runs_task ON runs(task_id);
`
