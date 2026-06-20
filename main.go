// Command sx is a tmux session + git-worktree switcher TUI.
//
// Two roles in one binary (the deferred-switch design):
//
//	sx          launcher: opens the TUI in a tmux popup, then performs the
//	            switch-client AFTER the popup closes (reliable context).
//	sx menu     the Bubble Tea TUI body, run inside `tmux display-popup -E`.
//	            It writes the chosen session name to $SX_TARGET_FILE and exits;
//	            it never calls switch-client itself (unreliable inside a popup).
package main

import (
	"fmt"
	"os"

	"sx/internal/tmux"
	"sx/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "menu" {
		runMenu()
		return
	}
	runLauncher()
}

// runLauncher opens the popup, then switches to whatever the menu recorded.
func runLauncher() {
	paneDir := tmux.PaneCurrentPath()
	if paneDir == "" {
		paneDir, _ = os.Getwd()
	}

	target, err := os.CreateTemp("", "sx-target.*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sx:", err)
		os.Exit(1)
	}
	targetPath := target.Name()
	target.Close()
	defer os.Remove(targetPath)

	self, err := os.Executable()
	if err != nil {
		self = "sx"
	}

	env := []string{
		"SX_PANE_PATH=" + paneDir,
		"SX_TARGET_FILE=" + targetPath,
	}
	if err := tmux.DisplayPopup(" sx ", env, self+" menu"); err != nil {
		fmt.Fprintln(os.Stderr, "sx:", err)
		os.Exit(1)
	}

	// Popup is closed — perform the deferred switch from this reliable context.
	data, _ := os.ReadFile(targetPath)
	name := firstLine(string(data))
	if name != "" && tmux.HasSession(name) {
		_ = tmux.SwitchClient(name)
	}
}

// runMenu is the popup body: run the TUI, then record the chosen session.
func runMenu() {
	paneDir := os.Getenv("SX_PANE_PATH")
	if paneDir == "" {
		paneDir, _ = os.Getwd()
	}

	model, err := tea.NewProgram(ui.New(paneDir), tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sx:", err)
		os.Exit(1)
	}

	if m, ok := model.(ui.Model); ok && m.Target != "" {
		if f := os.Getenv("SX_TARGET_FILE"); f != "" {
			_ = os.WriteFile(f, []byte(m.Target+"\n"), 0o600)
		}
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
