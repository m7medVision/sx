package ui

import (
	"testing"

	"github.com/m7medVision/sx/internal/tmux"
)

func TestResolveFocus(t *testing.T) {
	sessions := []tmux.Session{
		{Name: "dotfiles", Path: "/home/u/dotfiles"},
		{Name: "manara-dev", Path: "/home/u/manara"},
		{Name: "other", Path: "/home/u/other"},
	}
	t.Setenv("SX_CLIENT_SESSION", "manara-dev")
	name, cur := resolveFocus(sessions, "")
	if name != "manara-dev" || cur != 0 {
		t.Fatalf("got %q @ %d", name, cur)
	}
}
