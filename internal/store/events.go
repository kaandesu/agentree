package store

import (
	"context"
	"encoding/json"
)

// Event type constants. These are the "meaningful events" the supervisor and
// the dashboard activity feed consume. AppendEvent persists them; Emit is a
// typed convenience that JSON-encodes a payload map.
const (
	EventIdeaAdded       = "idea.added"
	EventIdeaDeleted     = "idea.deleted"
	EventTaskRemoved     = "task.removed"
	EventPlanReady       = "plan.ready"
	EventPlanSplit       = "plan.split"
	EventWorktreeCreated = "worktree.created"
	EventAgentExited     = "agent.exited"
	EventPROpened        = "pr.opened"
	EventMerged          = "merge.done"
	EventDiscarded       = "worktree.discarded"
)

// Emit appends a typed event with a JSON payload. Errors are returned for the
// caller to ignore (events are best-effort telemetry, never on a hot path).
func (s *Store) Emit(ctx context.Context, typ string, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte("{}")
	}
	return s.AppendEvent(ctx, typ, string(b))
}

// RecentEvents returns the most recent events, newest first, capped at limit.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, payload, created_at FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
