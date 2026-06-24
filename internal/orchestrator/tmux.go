package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// TmuxManager drives tmux to host one interactive agent CLI (claude) per slot.
// agentree spawns these long-running CLIs in tmux rather than embedded PTYs: the
// agent gets a real terminal, sidestepping in-app input forwarding entirely.
//
// It runs in one of two modes:
//
//   - inherited ($TMUX set): agentree is itself a tmux pane. Agents are spawned
//     as sibling PANES (split-window) in agentree's own window, so the user sees
//     agentree and every agent tiled together. This is the normal mode — at
//     startup agentree re-execs itself inside tmux (see main.go) precisely so it
//     can be a pane alongside its agents.
//   - standalone (no $TMUX): a dedicated tmux server (its own -L socket) hosts
//     one WINDOW per agent. Used only as a degraded fallback when tmux can't be
//     bootstrapped, and by tests. The user would attach to interact.
//
// Both modes expose the same surface (NewWindow/ListWindows/KillWindow/Capture);
// the "id" returned and threaded back is an opaque tmux target — a pane id (%N)
// in inherited mode, a window id (@N) in standalone mode.
type TmuxManager struct {
	Socket  string // -L socket name (standalone mode only)
	Session string // session that holds all agent windows (standalone mode)

	// inherited mode state.
	inherited  bool
	owns       bool   // we created this tmux server (bootstrap), so we may set session options
	selfPane   string // agentree's own pane id ($TMUX_PANE)
	selfWindow string // window agentree lives in; agents split into it
}

// sidebarWidth is agentree's pane width (columns) in inherited mode; the
// main-vertical layout pins agentree to the left as a control sidebar and tiles
// the agent panes in the remaining width.
const sidebarWidth = 44

// NewTmuxManager returns a standalone manager bound to the given socket name
// (defaulting to "agentree"). All windows live in a single session, also named
// "agentree". Used as the no-tmux fallback and by tests.
func NewTmuxManager(socket string) *TmuxManager {
	if strings.TrimSpace(socket) == "" {
		socket = "agentree"
	}
	return &TmuxManager{Socket: socket, Session: "agentree"}
}

// NewInheritedTmuxManager returns a manager that drives the tmux server agentree
// is already running inside ($TMUX). Agents are spawned as panes in agentree's
// window. owns reports whether agentree created this server (via the startup
// bootstrap), in which case it's safe to flip session-global options like mouse.
func NewInheritedTmuxManager(owns bool) *TmuxManager {
	return &TmuxManager{inherited: true, owns: owns, selfPane: os.Getenv("TMUX_PANE")}
}

// Inherited reports whether agentree runs as a tmux pane (vs. standalone).
func (t *TmuxManager) Inherited() bool { return t.inherited }

// TmuxAvailable reports whether the tmux binary is on PATH.
func TmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Window is one tmux unit agentree tracks: a pane (inherited mode) or a window
// (standalone mode). The struct name is historical; ID is whichever tmux id.
type Window struct {
	ID           string    // tmux pane (%N) or window (@N) id
	Name         string    // pane title / window name (our slug)
	Dead         bool      // command exited but the unit was retained
	Status       int       // exit code when Dead
	PID          int       // pane process id
	LastActivity time.Time // last activity (standalone only; zero for panes)
}

// tmux runs a tmux subcommand and returns its combined output. In standalone
// mode it targets agentree's private socket (-L); in inherited mode it talks to
// the inherited server ($TMUX) directly so commands land on the same server that
// is displaying agentree.
func (t *TmuxManager) tmux(ctx context.Context, args ...string) (string, error) {
	var full []string
	if t.inherited {
		full = args
	} else {
		full = append([]string{"-L", t.Socket}, args...)
	}
	cmd := exec.CommandContext(ctx, "tmux", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Ensure prepares tmux so agent units stay inspectable after their command
// exits (remain-on-exit). In inherited mode it also records agentree's own
// window so agents can be split into it.
func (t *TmuxManager) Ensure(ctx context.Context) error {
	if !TmuxAvailable() {
		return fmt.Errorf("tmux not found on PATH")
	}
	if t.inherited {
		if t.selfWindow != "" {
			return nil // already discovered; idempotent across flows.
		}
		// Resolve the window agentree lives in from its own pane.
		out, err := t.tmux(ctx, "display-message", "-p", "-t", t.selfPane, "#{window_id}")
		if err != nil {
			return err
		}
		t.selfWindow = strings.TrimSpace(out)
		// Scope remain-on-exit to agentree's window so dead agent panes linger
		// (final output + exit status) without touching the user's other windows.
		if _, err := t.tmux(ctx, "set-window-option", "-t", t.selfWindow, "remain-on-exit", "on"); err != nil {
			return err
		}
		// Only enable mouse on a server we created — never override the user's
		// own tmux config when agentree was launched inside their session.
		if t.owns {
			_, _ = t.tmux(ctx, "set-option", "-g", "mouse", "on")
		}
		return nil
	}
	// Standalone: boot the server + base session (idempotent).
	if _, err := t.tmux(ctx, "has-session", "-t", t.Session); err != nil {
		if _, err := t.tmux(ctx, "new-session", "-d", "-s", t.Session, "-x", "220", "-y", "50"); err != nil {
			return err
		}
	}
	if _, err := t.tmux(ctx, "set-option", "-g", "remain-on-exit", "on"); err != nil {
		return err
	}
	return nil
}

// NewWindow creates a unit running argv in dir and returns its tmux id. In
// inherited mode it splits a new pane into agentree's window and re-tiles
// (agentree stays the left sidebar); in standalone mode it opens a window.
func (t *TmuxManager) NewWindow(ctx context.Context, name, dir string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("tmux new-window: empty argv")
	}
	if t.inherited {
		args := []string{"split-window", "-t", t.selfWindow, "-c", dir, "-P", "-F", "#{pane_id}", "--"}
		args = append(args, argv...)
		out, err := t.tmux(ctx, args...)
		if err != nil {
			return "", err
		}
		paneID := strings.TrimSpace(out)
		// Label the pane (handy in tmux UIs) and re-tile: agentree pinned left.
		_, _ = t.tmux(ctx, "select-pane", "-t", paneID, "-T", name)
		_, _ = t.tmux(ctx, "set-window-option", "-t", t.selfWindow, "main-pane-width", strconv.Itoa(sidebarWidth))
		_, _ = t.tmux(ctx, "select-layout", "-t", t.selfWindow, "main-vertical")
		return paneID, nil
	}
	args := []string{"new-window", "-t", t.Session, "-P", "-F", "#{window_id}",
		"-n", name, "-c", dir, "--"}
	args = append(args, argv...)
	out, err := t.tmux(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ListWindows returns the agent units agentree tracks. In inherited mode it
// lists the panes in agentree's window (excluding agentree's own pane); in
// standalone mode it lists the session's windows. Callers filter to the ids
// they spawned.
func (t *TmuxManager) ListWindows(ctx context.Context) ([]Window, error) {
	if t.inherited {
		out, err := t.tmux(ctx, "list-panes", "-t", t.selfWindow, "-F",
			"#{pane_id}|#{pane_title}|#{pane_dead}|#{pane_dead_status}|#{pane_pid}")
		if err != nil {
			return nil, err
		}
		var ws []Window
		sc := bufio.NewScanner(strings.NewReader(out))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			f := strings.SplitN(line, "|", 5)
			if len(f) < 5 {
				continue
			}
			if f[0] == t.selfPane {
				continue // agentree's own pane is not an agent
			}
			w := Window{ID: f[0], Name: f[1], Dead: f[2] == "1"}
			w.Status, _ = strconv.Atoi(f[3])
			w.PID, _ = strconv.Atoi(f[4])
			// tmux 3.x exposes no per-pane activity time; idle detection is
			// dropped in inherited mode (the panes are on screen anyway).
			ws = append(ws, w)
		}
		return ws, nil
	}
	out, err := t.tmux(ctx, "list-windows", "-t", t.Session, "-F",
		"#{window_id}|#{window_name}|#{pane_dead}|#{pane_dead_status}|#{pane_pid}|#{window_activity}")
	if err != nil {
		return nil, err
	}
	var ws []Window
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 6)
		if len(f) < 6 {
			continue
		}
		w := Window{ID: f[0], Name: f[1], Dead: f[2] == "1"}
		w.Status, _ = strconv.Atoi(f[3])
		w.PID, _ = strconv.Atoi(f[4])
		if secs, e := strconv.ParseInt(f[5], 10, 64); e == nil && secs > 0 {
			w.LastActivity = time.Unix(secs, 0)
		}
		ws = append(ws, w)
	}
	return ws, nil
}

// Capture returns the current visible contents of a unit's pane (works for live
// and dead-but-retained panes/windows).
func (t *TmuxManager) Capture(ctx context.Context, id string) (string, error) {
	return t.tmux(ctx, "capture-pane", "-t", id, "-p")
}

// KillWindow removes a unit. In inherited mode it kills the agent pane (and
// re-tiles so agentree reclaims the space); in standalone mode it kills the
// window.
func (t *TmuxManager) KillWindow(ctx context.Context, id string) error {
	if t.inherited {
		if _, err := t.tmux(ctx, "kill-pane", "-t", id); err != nil {
			return err
		}
		// Re-tile the survivors; harmless if agentree is now the only pane.
		_, _ = t.tmux(ctx, "select-layout", "-t", t.selfWindow, "main-vertical")
		return nil
	}
	_, err := t.tmux(ctx, "kill-window", "-t", id)
	return err
}

// FocusNext moves tmux focus to the next agent pane (inherited mode), a
// keyboard convenience since agentree and its agents share one window. No-op in
// standalone mode.
func (t *TmuxManager) FocusNext(ctx context.Context) error {
	if !t.inherited {
		return nil
	}
	_, err := t.tmux(ctx, "select-pane", "-t", ":.+")
	return err
}

// AttachArgs returns the argv (after the tmux binary) to attach to the
// standalone session. Empty in inherited mode (agentree is already visible).
func (t *TmuxManager) AttachArgs(windowID string) []string {
	if t.inherited {
		return nil
	}
	args := []string{"-L", t.Socket, "attach", "-t", t.Session}
	if windowID != "" {
		args = append(args, ";", "select-window", "-t", windowID)
	}
	return args
}

// AttachCommand is the human-runnable command to attach from another terminal.
// Empty in inherited mode, where agents are already on screen beside agentree.
func (t *TmuxManager) AttachCommand() string {
	if t.inherited {
		return ""
	}
	return "tmux -L " + t.Socket + " attach -t " + t.Session
}

// AttachExecCmd returns an *exec.Cmd that attaches to the standalone session,
// suitable for tea.ExecProcess. Nil in inherited mode.
func (t *TmuxManager) AttachExecCmd(windowID string) *exec.Cmd {
	if t.inherited {
		return nil
	}
	return exec.Command("tmux", t.AttachArgs(windowID)...)
}

// Kill tears down the standalone server. Used by tests; the app intentionally
// leaves tmux running on exit so long builds survive. No-op in inherited mode
// (that's the user's server).
func (t *TmuxManager) Kill(ctx context.Context) error {
	if t.inherited {
		return nil
	}
	_, err := t.tmux(ctx, "kill-server")
	return err
}
