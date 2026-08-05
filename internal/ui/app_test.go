package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m7medVision/sx/internal/agent"
	"github.com/m7medVision/sx/internal/inventory"
	"github.com/m7medVision/sx/internal/tmux"

	"github.com/awesome-gocui/gocui"
)

func TestSessionInventorySummaryIncludesGitWorktreeAndActivity(t *testing.T) {
	row := inventory.Session{
		Branch:         "feature/inventory",
		Dirty:          true,
		LinkedWorktree: true,
		WorktreePath:   "/repo/.worktrees/inventory",
		HasGitContext:  true,
		Activity:       time.Date(2026, 8, 5, 9, 58, 0, 0, time.UTC),
	}
	got := sessionInventorySummary(row, time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{"feature/inventory", "dirty", "worktree", "/repo/.worktrees/inventory", "active 2m ago"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, missing %q", got, want)
		}
	}
}

func TestSessionInventorySummaryHandlesMissingMetadata(t *testing.T) {
	if got := sessionInventorySummary(inventory.Session{}, time.Now()); got != "" {
		t.Fatalf("summary = %q, want empty", got)
	}
}

func TestWorktreeSourcePrefersTypedInput(t *testing.T) {
	if got := selectedWorktreeSource("upstream/feature/x", []string{"main", "origin/feature/x"}, 1); got != "upstream/feature/x" {
		t.Fatalf("selected source = %q", got)
	}
	if got := selectedWorktreeSource("  ", []string{"main", "origin/feature/x"}, 1); got != "origin/feature/x" {
		t.Fatalf("selected source = %q", got)
	}
}

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

func TestNextAttentionWrapsAndSkipsOrdinarySessions(t *testing.T) {
	app := &App{cursor: 1, sessionInventory: inventory.Snapshot{Sessions: []inventory.Session{
		{Name: "blocked", Assessment: agent.Assessment{State: agent.Blocked}},
		{Name: "ordinary", Assessment: agent.Assessment{State: agent.Working}},
		{Name: "unread", Attention: &inventory.Attention{}},
	}}}
	if err := app.nextAttention(); err != nil {
		t.Fatal(err)
	}
	if app.cursor != 2 {
		t.Fatalf("cursor = %d, want unread session", app.cursor)
	}
	if err := app.nextAttention(); err != nil {
		t.Fatal(err)
	}
	if app.cursor != 0 {
		t.Fatalf("cursor = %d, want wrapped blocked session", app.cursor)
	}
}

func TestVisitingSessionAcknowledgesItsLifecycleRecord(t *testing.T) {
	path := t.TempDir() + "/events.json"
	t.Setenv("SX_AGENT_EVENT_STORE", path)
	store := agent.Store{Path: path}
	if err := store.Put(agent.Event{Session: "api", State: agent.Completed, Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	app := &App{mode: modeList, sessionInventory: inventory.Snapshot{Sessions: []inventory.Session{{Name: "api"}}}}
	if err := app.onEnter(nil, nil); !errors.Is(err, gocui.ErrQuit) {
		t.Fatalf("onEnter error = %v", err)
	}
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !events["api"].Acknowledged || events["api"].Summary != "done" {
		t.Fatalf("event = %#v", events["api"])
	}
}

func TestSessionInventorySummaryShowsUnreadAttentionTime(t *testing.T) {
	row := inventory.Session{Attention: &inventory.Attention{At: time.Date(2026, 8, 5, 9, 58, 0, 0, time.UTC)}}
	if got := sessionInventorySummary(row, time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)); !strings.Contains(got, "attention unread 2m ago") {
		t.Fatalf("summary = %q", got)
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

// TestFitPreviewLazygitFrame checks that box-drawing frame characters survive
// for non-agent TUIs (the regression the original compaction broke).
func TestFitPreviewLazygitFrame(t *testing.T) {
	// A ~10-row lazygit-style capture with a full box frame.
	raw := "" +
		"┌──────────────────────────────┐\n" +
		"│ Status     Files     Branches │\n" +
		"├──────────────────────────────┤\n" +
		"│ app.go      M 3     main      │\n" +
		"│ ui.go       A 1     feature/x │\n" +
		"│ ▸ README.md    ??  develop    │\n" +
		"│                              │\n" +
		"└──────────────────────────────┘\n" +
		"Press <esc> to return to menu\n"

	// Width comfortably fits; height via tailRows keeps all of it.
	got := fitPreview(raw, 40, 20)
	joined := strings.Join(got, "\n")
	for _, want := range []string{"┌", "┐", "└", "┘", "├", "┤", "│", "app.go", "README.md", "esc"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("frame/content rune %q missing from preview: %q", want, got)
		}
	}
	// Inner row must keep its spacing, not be space-folded into adjacent cells.
	if !strings.Contains(got[3], "app.go") {
		t.Fatalf("inner row mangled: %q", got[3])
	}
}

// TestFitPreviewBtopBox exercises multi-column box-drawing layouts.
func TestFitPreviewBtopBox(t *testing.T) {
	raw := "" +
		"┌─────────┐ ┌─────────┐ ┌─────────┐\n" +
		"│ CPU      │ │ Mem      │ │ Net      │\n" +
		"│ ████ 23% │ │ ██ 41%   │ │ ▲ 1.2MB  │\n" +
		"└─────────┘ └─────────┘ └─────────┘\n"

	got := fitPreview(raw, 40, 10)
	joined := strings.Join(got, "\n")
	for _, want := range []string{"┌", "┐", "└", "┘", "██", "CPU", "Mem", "Net"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("btop rune %q missing from preview: %q", want, got)
		}
	}
}
