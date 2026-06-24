package orchestrator

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// TmuxManager drives a dedicated tmux server (its own socket) that hosts one
// window per plan/agent session. agentree spawns long-running interactive CLIs
// (claude) as tmux windows rather than embedded PTYs: the user attaches to a
// real terminal to interact, sidestepping in-app input forwarding entirely and
// getting tmux's native window cycling for free.
//
// A private socket (tmux -L <socket>) isolates agentree's windows from the
// user's normal tmux server and lets us flip server-global options
// (remain-on-exit) without disturbing their setup.
type TmuxManager struct {
	Socket  string // -L socket name
	Session string // session that holds all agent windows
}

// NewTmuxManager returns a manager bound to the given socket name (defaulting
// to "agentree"). All windows live in a single session, also named "agentree",
// so the user can attach once and tab-cycle between concurrent agents.
func NewTmuxManager(socket string) *TmuxManager {
	if strings.TrimSpace(socket) == "" {
		socket = "agentree"
	}
	return &TmuxManager{Socket: socket, Session: "agentree"}
}

// TmuxAvailable reports whether the tmux binary is on PATH.
func TmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Window is one tmux window in agentree's session.
type Window struct {
	ID     string // tmux window id, e.g. "@3"
	Name   string // window name (our slug)
	Dead   bool   // command exited but window retained (remain-on-exit)
	Status int    // exit code when Dead
	PID    int    // pane process id
}

// tmux runs a tmux subcommand against our dedicated socket and returns its
// combined output.
func (t *TmuxManager) tmux(ctx context.Context, args ...string) (string, error) {
	full := append([]string{"-L", t.Socket}, args...)
	cmd := exec.CommandContext(ctx, "tmux", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Ensure boots the server + base session (idempotent) and enables
// remain-on-exit so dead agent windows stay inspectable (final output + exit
// status) instead of vanishing the instant their command exits.
func (t *TmuxManager) Ensure(ctx context.Context) error {
	if !TmuxAvailable() {
		return fmt.Errorf("tmux not found on PATH")
	}
	// has-session exits non-zero if the server or session is absent.
	if _, err := t.tmux(ctx, "has-session", "-t", t.Session); err != nil {
		if _, err := t.tmux(ctx, "new-session", "-d", "-s", t.Session, "-x", "220", "-y", "50"); err != nil {
			return err
		}
	}
	// Server-global (safe on our private socket): keep dead windows around.
	if _, err := t.tmux(ctx, "set-option", "-g", "remain-on-exit", "on"); err != nil {
		return err
	}
	return nil
}

// NewWindow creates a window named name running argv in dir and returns its
// tmux window id (e.g. "@3").
func (t *TmuxManager) NewWindow(ctx context.Context, name, dir string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("tmux new-window: empty argv")
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

// ListWindows returns all windows in agentree's session, including the base
// shell window. Callers filter to the windows they spawned by id.
func (t *TmuxManager) ListWindows(ctx context.Context) ([]Window, error) {
	out, err := t.tmux(ctx, "list-windows", "-t", t.Session, "-F",
		"#{window_id}|#{window_name}|#{pane_dead}|#{pane_dead_status}|#{pane_pid}")
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
		w := Window{ID: f[0], Name: f[1], Dead: f[2] == "1"}
		w.Status, _ = strconv.Atoi(f[3])
		w.PID, _ = strconv.Atoi(f[4])
		ws = append(ws, w)
	}
	return ws, nil
}

// Capture returns the current visible contents of a window's pane (works for
// live and dead-but-retained panes).
func (t *TmuxManager) Capture(ctx context.Context, windowID string) (string, error) {
	return t.tmux(ctx, "capture-pane", "-t", windowID, "-p")
}

// KillWindow removes a window (live or dead).
func (t *TmuxManager) KillWindow(ctx context.Context, windowID string) error {
	_, err := t.tmux(ctx, "kill-window", "-t", windowID)
	return err
}

// AttachArgs returns the argv (after the tmux binary) to attach to the session.
// If windowID is non-empty, the attach selects that window first so the user
// lands directly on it; from there tmux's own bindings cycle between agents.
func (t *TmuxManager) AttachArgs(windowID string) []string {
	args := []string{"-L", t.Socket, "attach", "-t", t.Session}
	if windowID != "" {
		args = append(args, ";", "select-window", "-t", windowID)
	}
	return args
}

// AttachCommand is the human-runnable command to attach from another terminal.
func (t *TmuxManager) AttachCommand() string {
	return "tmux -L " + t.Socket + " attach -t " + t.Session
}

// AttachExecCmd returns an *exec.Cmd that attaches to the session (selecting
// windowID first if given), suitable for tea.ExecProcess to hand the terminal
// over to tmux.
func (t *TmuxManager) AttachExecCmd(windowID string) *exec.Cmd {
	return exec.Command("tmux", t.AttachArgs(windowID)...)
}

// Kill tears down the entire dedicated server. Used by tests; the app
// intentionally leaves the server running on exit so long builds survive and
// the user can reconnect.
func (t *TmuxManager) Kill(ctx context.Context) error {
	_, err := t.tmux(ctx, "kill-server")
	return err
}
