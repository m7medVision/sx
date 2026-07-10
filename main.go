// Command sx is a tmux session + git-worktree switcher TUI.
//
// Two roles in one binary (the deferred-switch design):
//
//	sx          launcher: opens the TUI in a tmux popup, then performs the
//	            switch-client AFTER the popup closes (reliable context).
//	sx menu     the TUI body, run inside `tmux display-popup -E`.
//	            It writes the chosen session name to $SX_TARGET_FILE and exits;
//	            it never calls switch-client itself (unreliable inside a popup).
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/m7medVision/sx/internal/tmux"
	"github.com/m7medVision/sx/internal/ui"
)

// version is the build version, injected at release time via -ldflags
// "-X main.version=...". It defaults to "dev" for local builds.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "menu":
			runMenu()
			return
		case "version", "--version", "-v":
			fmt.Println("sx", resolveVersion())
			return
		}
	}
	runLauncher()
}

// resolveVersion returns the build version. GoReleaser binaries carry it via
// ldflags; `go install ...@version` doesn't set ldflags, so we fall back to the
// module version Go embeds in the build info. Local builds report "dev".
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
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
		"SX_CLIENT_SESSION=" + tmux.ClientSession(),
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

	target, err := ui.Run(paneDir, resolveVersion())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sx:", err)
		os.Exit(1)
	}
	if target != "" {
		if f := os.Getenv("SX_TARGET_FILE"); f != "" {
			_ = os.WriteFile(f, []byte(target+"\n"), 0o600)
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
