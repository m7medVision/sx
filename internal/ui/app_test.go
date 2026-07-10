package ui

import (
	"strings"
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

func TestFitPreviewClaudeWelcome(t *testing.T) {
	// Physical layout: header, empty middle, prompt + footer at bottom.
	var b strings.Builder
	b.WriteString("Claude Code v2.1.205 " + strings.Repeat("─", 40) + "\n")
	b.WriteString("  Welcome back Mohammed!\n")
	b.WriteString("  Haiku 4.5 · Claude Max\n")
	b.WriteString("  ~/repo/sx\n")
	b.WriteString("  Tips for getting started\n")
	for i := 0; i < 25; i++ {
		b.WriteString("\n")
	}
	b.WriteString("  ⚠ 2 MCP servers need authentication · run /mcp\n")
	for i := 0; i < 8; i++ {
		b.WriteString("\n")
	}
	b.WriteString("  ❯ Try \"fix typecheck errors\"\n")
	b.WriteString("  ⏸ manual mode on · ? for shortcuts · ← for agents\n")

	got := fitPreview(b.String(), 70, 16)
	if len(got) < 4 {
		t.Fatalf("too few lines for Claude TUI: %d %q", len(got), got)
	}
	joined := strings.ToLower(stripANSI(strings.Join(got, "\n")))
	for _, want := range []string{"claude code", "welcome", "? for shortcuts"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in preview: %q", want, got)
		}
	}
	// Prompt line should survive (not chrome-only filter).
	if !strings.Contains(joined, "try") && !strings.Contains(joined, "typecheck") && !strings.Contains(joined, "mcp") {
		t.Fatalf("missing prompt/alert content: %q", got)
	}
}

func TestFitPreviewOpencodeNoMiddleDump(t *testing.T) {
	// 47-row pane: top header, middle dump only, bottom chrome.
	// Dump lives in rows 14..30 so top/bottom windows miss it.
	rows := make([]string, 47)
	for i := range rows {
		rows[i] = ""
	}
	for i := 0; i < 8; i++ {
		rows[i] = "session header line"
	}
	for i := 14; i < 31; i++ {
		rows[i] = `TMUX="" tmux -L smoketest show-option -gv foo`
	}
	rows[44] = "Build · MiniMax-M3"
	rows[45] = "Build · MiniMax-M3 OpenCode Go"
	rows[46] = "esc interrupt  53.6K (5%) · $0.21  ctrl+p commands"

	got := fitPreview(strings.Join(rows, "\n"), 60, 20)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "smoketest") {
		t.Fatalf("middle dump leaked: %q", got)
	}
	if !strings.Contains(joined, "esc interrupt") {
		t.Fatalf("missing footer: %q", got)
	}
	if !strings.Contains(joined, "Build") {
		t.Fatalf("missing build: %q", got)
	}
}

func TestFitPreviewPlainShell(t *testing.T) {
	raw := "line1\nline2\nline3\nline4\nline5\n"
	got := fitPreview(raw, 40, 3)
	if len(got) != 3 {
		t.Fatalf("want 3 lines, got %d %q", len(got), got)
	}
	if stripANSI(got[0]) != "line3" || stripANSI(got[2]) != "line5" {
		t.Fatalf("got %q", got)
	}
}

func TestIsAgentPane(t *testing.T) {
	if !isAgentPane("foo ? for shortcuts bar") {
		t.Fatal("expected agent")
	}
	if isAgentPane("just a normal shell prompt $") {
		t.Fatal("expected non-agent")
	}
}
