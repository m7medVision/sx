// Package tmux is a thin wrapper over the `tmux` CLI used by sx.
package tmux

import (
	"os/exec"
	"strings"
)

// run executes a tmux command and returns trimmed stdout.
func run(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// Session is a tmux session with a little context for the UI.
type Session struct {
	Name string
	Path string // pane_current_path of the session's active pane
}

// ListSessions returns all tmux sessions (empty slice when there are none).
func ListSessions() []Session {
	out, err := run("list-sessions", "-F", "#{session_name}\t#{pane_current_path}")
	if err != nil || out == "" {
		return nil
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		name, path, _ := strings.Cut(line, "\t")
		if name != "" {
			sessions = append(sessions, Session{Name: name, Path: path})
		}
	}
	return sessions
}

// CapturePane returns a colored snapshot of the session's active pane for the
// live preview (-e keeps ANSI; -p writes to stdout). The UI compacts wide
// agent panes while preserving colors.
func CapturePane(session string) string {
	out, _ := run("capture-pane", "-ep", "-t", session)
	return out
}

// ActiveWindow returns the name of the session's current window, or "".
func ActiveWindow(session string) string {
	out, _ := run("display", "-t", session, "-p", "#{window_name}")
	return out
}

// CapturePlain returns the session's active pane as plain text (no ANSI), used
// for agent-state detection where escape sequences would break substring
// matching.
func CapturePlain(session string) string {
	out, _ := run("capture-pane", "-p", "-t", session)
	return out
}

// HasSession reports whether a session with the given name exists.
func HasSession(name string) bool {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	return err == nil
}

// NewSession creates a detached session rooted at dir (no-op if it exists).
func NewSession(name, dir string) error {
	if HasSession(name) {
		return nil
	}
	return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", dir).Run()
}

// KillSession kills a session by name.
func KillSession(name string) error {
	return exec.Command("tmux", "kill-session", "-t", name).Run()
}

// SwitchClient switches the current client to the named session.
func SwitchClient(name string) error {
	return exec.Command("tmux", "switch-client", "-t", name).Run()
}

// ClientSession returns the session the current client is attached to.
func ClientSession() string {
	out, _ := run("display", "-p", "#{client_session}")
	return out
}

// PaneCurrentPath returns the working directory of the current pane.
func PaneCurrentPath() string {
	out, _ := run("display", "-p", "#{pane_current_path}")
	return out
}

// DisplayPopup runs `tmux display-popup -E` with the given env and command,
// blocking until the popup closes. env entries are "KEY=value".
func DisplayPopup(title string, env []string, command string) error {
	args := []string{"display-popup", "-E", "-w", "80%", "-h", "70%", "-b", "rounded", "-T", title}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, command)
	cmd := exec.Command("tmux", args...)
	return cmd.Run()
}
