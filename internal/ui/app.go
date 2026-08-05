// Package ui implements the sx TUI (the `sx menu` popup body) with gocui.
package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/m7medVision/sx/internal/agent"
	"github.com/m7medVision/sx/internal/config"
	"github.com/m7medVision/sx/internal/git"
	"github.com/m7medVision/sx/internal/inventory"
	"github.com/m7medVision/sx/internal/tmux"
	"github.com/m7medVision/sx/internal/update"

	"github.com/awesome-gocui/gocui"
	"github.com/mattn/go-runewidth"
)

const previewPollInterval = 500 * time.Millisecond

type mode int

const (
	modeList mode = iota
	modeNewSession
	modeBranchPick
	modeConfirm
	modeHelp
	modeConfig
	modeConfigEdit
	modeCfgPick
)

// App holds TUI state. Target is the session to switch to after the popup closes.
type App struct {
	mode mode

	paneDir  string
	inRepo   bool
	repoRoot string
	attached string

	sessionInventory inventory.Snapshot
	cursor           int

	markers agent.Markers

	// promptSeed is written into the prompt view once after a mode switch.
	promptSeed   string
	promptSeeded bool

	branches []string
	filtered []string
	bcursor  int

	confirmMsg    string
	pendingKill   inventory.Session
	pendingWtRoot string

	status string
	Target string

	version      string
	updateLatest string
	updateAvail  bool

	// Preview: cache, window label, and user toggle (p).
	previewOn        bool
	previewCacheName string
	previewCache     string
	previewWin       string
	// Config view: which scope is active and the edited buffers for each.
	cfgScope   config.Scope
	cfgGlobal  config.Values
	cfgProject config.Values

	cfgCursor     int // index into the config rows (0=dir,1=copy,2=symlink)
	cfgEntry      int // index into the focused list row's entries (copy/symlink)
	cfgEditSeed   string
	cfgEditSeeded bool

	// File picker (mini-fzf) for adding copy/symlink entries.
	cfgPickTarget      int      // 1=copy, 2=symlink
	cfgPickQuery       string   // editable query at top of picker
	cfgPickFiles       []string // all repo files, relative
	cfgPickFiltered    []int    // indices into cfgPickFiles after fuzzy filter
	cfgPickCursor      int      // cursor in filtered list
	cfgPickMarked      map[int]bool
	cfgPickLoaded      bool
	cfgPickTruncated   bool
	cfgPickLastAdded   int
	cfgPickLastAddedAt time.Time
}

// configRow is one editable line in the config view.
type configRow struct {
	label    string
	get      func(v config.Values) string
	set      func(v *config.Values, val string)
	editable bool // prompt-for-text rows; list rows are edited via a/d
}

// Run starts the TUI and returns the chosen session name (may be empty).
func Run(paneDir, version string) (string, error) {
	// OutputTrue keeps 24-bit colors from agent UIs (opencode/Claude).
	g, err := gocui.NewGui(gocui.OutputTrue, true)
	if err != nil {
		g, err = gocui.NewGui(gocui.Output256, true)
	}
	if err != nil {
		return "", err
	}
	defer g.Close()

	g.Cursor = true
	g.InputEsc = true

	app := newApp(paneDir, version)
	g.SetManagerFunc(app.layout)
	if err := app.bindKeys(g); err != nil {
		return "", err
	}

	go app.checkUpdate(g)
	go app.pollPreview(g)

	if err := g.MainLoop(); err != nil && !errors.Is(err, gocui.ErrQuit) {
		return app.Target, err
	}
	return app.Target, nil
}

func newApp(paneDir, version string) *App {
	repoRoot := ""
	inRepo := git.IsRepo(paneDir)
	if inRepo {
		repoRoot, _ = git.RepoRoot(paneDir)
	}
	markers := config.Load(repoRoot).Markers()
	sessions := tmux.ListSessions()
	sessionInventory := buildInventory(sessions, markers)
	attached, cursor := resolveFocus(sessions, paneDir)

	gv, _ := config.LoadScope(mustGlobalPath())
	pv, _ := config.LoadScope(config.ProjectPath(repoRoot))
	gValues := config.FromConfig(gv)
	pValues := config.FromConfig(pv)

	return &App{
		mode:             modeList,
		paneDir:          paneDir,
		inRepo:           inRepo,
		repoRoot:         repoRoot,
		attached:         attached,
		sessionInventory: sessionInventory,
		cursor:           cursor,
		markers:          markers,
		version:          version,
		previewOn:        true,
		cfgGlobal:        gValues,
		cfgProject:       pValues,
	}
}

// resolveFocus finds the attached session and the list index to land the cursor
// on: the previous session in list order (wrap to last). Enter then switches
// away without moving the cursor first.
//
// Attached is resolved by, in order:
//  1. SX_CLIENT_SESSION from the launcher (set outside the popup — reliable)
//  2. tmux #{client_session} queried from this process
//  3. session whose pane path matches the launching pane's directory
func resolveFocus(sessions []tmux.Session, paneDir string) (attached string, cursor int) {
	if len(sessions) == 0 {
		return "", 0
	}

	attIdx := -1
	candidates := []string{
		os.Getenv("SX_CLIENT_SESSION"),
		tmux.ClientSession(),
	}
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		for i, s := range sessions {
			if s.Name == cand {
				attached, attIdx = cand, i
				break
			}
		}
		if attIdx >= 0 {
			break
		}
	}
	if attIdx < 0 && paneDir != "" {
		for i, s := range sessions {
			if s.Path == paneDir {
				attached, attIdx = s.Name, i
				break
			}
		}
	}
	if attIdx < 0 {
		// Unknown attached — keep cursor on first; attached label empty.
		return "", 0
	}

	// Previous session, wrap around.
	cursor = attIdx - 1
	if cursor < 0 {
		cursor = len(sessions) - 1
	}
	return attached, cursor
}

func (a *App) checkUpdate(g *gocui.Gui) {
	latest, newer := update.Check(a.version)
	g.Update(func(g *gocui.Gui) error {
		a.updateLatest = latest
		a.updateAvail = newer
		return nil
	})
}

// pollPreview refreshes the focused session's capture while the list is open so
// the preview feels live without keypresses.
func (a *App) pollPreview(g *gocui.Gui) {
	t := time.NewTicker(previewPollInterval)
	defer t.Stop()
	for range t.C {
		g.Update(func(g *gocui.Gui) error {
			if a.mode != modeList || !a.previewOn || len(a.sessionInventory.Sessions) == 0 {
				return nil
			}
			a.refreshPreview(true)
			return nil
		})
	}
}

// refreshPreview updates the cached capture for the cursor session.
// force=true always recaptures; force=false only when the session changed.
func (a *App) refreshPreview(force bool) {
	if len(a.sessionInventory.Sessions) == 0 {
		a.previewCacheName, a.previewCache, a.previewWin = "", "", ""
		return
	}
	name := a.sessionInventory.Sessions[a.cursor].Name
	if !force && name == a.previewCacheName {
		return
	}
	a.previewCacheName = name
	a.previewCache = tmux.CapturePane(name)
	a.previewWin = tmux.ActiveWindow(name)
}

func (a *App) invalidatePreview() {
	a.previewCacheName, a.previewCache, a.previewWin = "", "", ""
}

// ── layout ──────────────────────────────────────────────────────────

func (a *App) layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	if maxX < 10 || maxY < 5 {
		return nil
	}

	for _, name := range []string{"banner", "list", "preview", "prompt", "branches", "confirm", "help", "config", "cfgedit", "cfgpick", "cfgpicklist"} {
		if v, err := g.View(name); err == nil {
			v.Visible = false
		}
	}

	top := 0
	if a.updateAvail {
		if err := a.setView(g, "banner", 0, 0, maxX-1, 2); err != nil {
			return err
		}
		v, _ := g.View("banner")
		v.Visible = true
		v.Frame = false
		v.Clear()
		fmt.Fprintf(v, "\x1b[1;34m ▲ update available: %s → %s   go install github.com/m7medVision/sx@latest\x1b[0m",
			a.version, a.updateLatest)
		top = 2
	}

	helpH := 3
	if a.status != "" {
		helpH = 5
	}
	bodyBottom := maxY - helpH - 1
	if bodyBottom <= top {
		bodyBottom = top + 1
	}

	switch a.mode {
	case modeList:
		return a.layoutList(g, maxX, top, bodyBottom, maxY)
	case modeNewSession:
		return a.layoutNewSession(g, maxX, top, bodyBottom, maxY)
	case modeBranchPick:
		return a.layoutBranchPick(g, maxX, top, bodyBottom, maxY)
	case modeConfirm:
		return a.layoutConfirm(g, maxX, top, bodyBottom, maxY)
	case modeHelp:
		return a.layoutHelpView(g, maxX, top, bodyBottom, maxY)
	case modeConfig, modeConfigEdit:
		return a.layoutConfig(g, maxX, top, bodyBottom, maxY)
	case modeCfgPick:
		return a.layoutCfgPick(g, maxX, top, bodyBottom, maxY)
	}
	return nil
}

func (a *App) setView(g *gocui.Gui, name string, x0, y0, x1, y1 int) error {
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	_, err := g.SetView(name, x0, y0, x1, y1, 0)
	if err != nil && !errors.Is(err, gocui.ErrUnknownView) {
		return err
	}
	return nil
}

func (a *App) layoutList(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	showPreview := a.previewOn && maxX > 60
	listRight := maxX - 1
	if showPreview {
		listRight = maxX * 25 / 100
		if listRight < 20 {
			listRight = 20
		}
	}

	if err := a.setView(g, "list", 0, top, listRight, bodyBottom); err != nil {
		return err
	}
	list, _ := g.View("list")
	list.Visible = true
	list.Title = " tmux sessions "
	list.Highlight = true
	list.SelFgColor = gocui.ColorGreen | gocui.AttrBold
	list.Clear()

	if len(a.sessionInventory.Sessions) == 0 {
		fmt.Fprintln(list, "  (no sessions)")
	} else {
		nameCol := a.nameColWidth(listRight - 2)
		for i, s := range a.sessionInventory.Sessions {
			prefix := "  "
			if i == a.cursor {
				prefix = "› "
			}
			pad := nameCol - utf8.RuneCountInString(s.Name)
			if pad < 0 {
				pad = 0
			}
			line := prefix + s.Name + strings.Repeat(" ", pad) + "  " + badge(s.State)
			if s.Name == a.attached {
				line += " \x1b[90m(attached)\x1b[0m"
			}
			fmt.Fprintln(list, line)
			if i < len(a.sessionInventory.Sessions) {
				if summary := sessionInventorySummary(a.sessionInventory.Sessions[i], time.Now()); summary != "" {
					fmt.Fprintln(list, "  \x1b[90m"+summary+"\x1b[0m")
					continue
				}
			}
			fmt.Fprintln(list) // reserve a context row for each session
		}
		// Clear() resets the cursor; re-apply after writing lines.
		// gocui Highlight compares cy to *screen* y (after origin), not buffer y.
		_, h := list.Size()
		cursorY := a.cursor * 2
		oy := 0
		if cursorY >= h {
			oy = cursorY - h + 1
		}
		_ = list.SetOrigin(0, oy)
		_ = list.SetCursorUnrestricted(0, cursorY-oy)
	}
	if _, err := g.SetCurrentView("list"); err != nil {
		return err
	}

	if showPreview && a.previewOn && len(a.sessionInventory.Sessions) > 0 {
		if err := a.setView(g, "preview", listRight, top, maxX-1, bodyBottom); err != nil {
			return err
		}
		pv, _ := g.View("preview")
		pv.Visible = true
		pv.Wrap = false
		a.refreshPreview(false)
		title := " " + a.sessionInventory.Sessions[a.cursor].Name + " "
		if a.previewWin != "" {
			title = " " + a.sessionInventory.Sessions[a.cursor].Name + " · " + a.previewWin + " "
		}
		pv.Title = title
		pv.Clear()
		w, h := pv.Size()
		for _, ln := range fitPreview(a.previewCache, w, h) {
			fmt.Fprintln(pv, ln)
		}
	}

	return a.layoutHelp(g, maxX, bodyBottom, maxY, a.listHelp())
}

func (a *App) layoutNewSession(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	if err := a.setView(g, "prompt", 0, top, maxX-1, top+2); err != nil {
		return err
	}
	p, _ := g.View("prompt")
	p.Visible = true
	p.Title = " New session "
	p.Editable = true
	p.Editor = gocui.DefaultEditor
	a.seedPrompt(p)
	if _, err := g.SetCurrentView("prompt"); err != nil {
		return err
	}
	return a.layoutHelp(g, maxX, bodyBottom, maxY, " enter: create   esc: back")
}

func (a *App) layoutBranchPick(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	if err := a.setView(g, "prompt", 0, top, maxX-1, top+2); err != nil {
		return err
	}
	p, _ := g.View("prompt")
	p.Visible = true
	p.Title = " New worktree — pick or type a branch "
	p.Editable = true
	p.Editor = gocui.EditorFunc(a.branchEditor)
	a.seedPrompt(p)
	if _, err := g.SetCurrentView("prompt"); err != nil {
		return err
	}

	if err := a.setView(g, "branches", 0, top+3, maxX-1, bodyBottom); err != nil {
		return err
	}
	bv, _ := g.View("branches")
	bv.Visible = true
	bv.Highlight = true
	bv.SelFgColor = gocui.ColorGreen | gocui.AttrBold
	bv.Frame = false
	bv.Clear()
	limit := 13
	for i, br := range a.filtered {
		if i >= limit {
			fmt.Fprintln(bv, "  …")
			break
		}
		prefix := "  "
		if i == a.bcursor {
			prefix = "› "
		}
		fmt.Fprintln(bv, prefix+br)
	}
	if len(a.filtered) > 0 {
		_ = bv.SetCursor(0, a.bcursor)
	}
	return a.layoutHelp(g, maxX, bodyBottom, maxY, " enter: use selected (or typed name)   esc: back")
}

func (a *App) layoutConfirm(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	if err := a.setView(g, "confirm", 0, top, maxX-1, bodyBottom); err != nil {
		return err
	}
	v, _ := g.View("confirm")
	v.Visible = true
	v.Title = " Kill session "
	v.Clear()
	fmt.Fprintln(v, "")
	fmt.Fprintln(v, " "+a.confirmMsg)
	if _, err := g.SetCurrentView("confirm"); err != nil {
		return err
	}
	return a.layoutHelp(g, maxX, bodyBottom, maxY, " y: remove worktree + kill   n: kill only   esc: cancel")
}

// layoutHelpView renders a full-screen help overlay listing every keybinding.
// It hides the list and preview so nothing shows through behind the panel.
func (a *App) layoutHelpView(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	if err := a.setView(g, "help", 0, top, maxX-1, maxY-1); err != nil {
		return err
	}
	v, _ := g.View("help")
	v.Visible = true
	v.Frame = true
	v.Title = " sx help "
	v.Clear()

	type row struct {
		key, desc string
	}
	rows := []row{
		{"enter", "switch to the selected session"},
		{"ctrl-n", "new plain session (prompts for a name)"},
		{"ctrl-w", "new git-worktree session (in a git repo)"},
		{"ctrl-x", "kill the selected session"},
		{"up / k", "move selection up"},
		{"down / j", "move selection down"},
		{"p", "toggle the preview pane"},
		{"?", "close this help"},
		{"q / esc", "close this help / cancel"},
	}

	keyW := 0
	for _, r := range rows {
		if w := utf8.RuneCountInString(r.key); w > keyW {
			keyW = w
		}
	}
	for _, r := range rows {
		fmt.Fprintf(v, "  \x1b[1m%s\x1b[0m%s%s\n",
			r.key,
			strings.Repeat(" ", keyW-utf8.RuneCountInString(r.key)+2),
			r.desc)
	}
	if a.status != "" {
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "\x1b[31m "+a.status+"\x1b[0m")
	}
	if _, err := g.SetCurrentView("help"); err != nil {
		return err
	}
	return nil
}

// layoutConfig renders the config editor for the active scope (global or
// project). In modeConfigEdit a prompt overlay sits at the top for the line
// being edited.
func (a *App) layoutConfig(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	scopeName := "global"
	path := mustGlobalPath()
	if a.cfgScope == config.ProjectScope {
		scopeName = "project"
		path = config.ProjectPath(a.repoRoot)
	}

	headerY := top
	if a.mode == modeConfigEdit {
		if err := a.setView(g, "cfgedit", 0, top, maxX-1, top+2); err != nil {
			return err
		}
		p, _ := g.View("cfgedit")
		p.Visible = true
		p.Title = " Edit: " + a.cfgRows()[a.cfgCursor].label + " "
		p.Editable = true
		p.Editor = gocui.DefaultEditor
		a.seedCfgEdit(p)
		if _, err := g.SetCurrentView("cfgedit"); err != nil {
			return err
		}
		headerY = top + 3
	}

	if err := a.setView(g, "config", 0, headerY, maxX-1, bodyBottom); err != nil {
		return err
	}
	v, _ := g.View("config")
	v.Visible = true
	v.Highlight = true
	v.SelFgColor = gocui.ColorGreen | gocui.AttrBold
	v.Frame = true
	v.Title = " config · " + scopeName + " "
	v.Clear()
	if a.mode != modeConfigEdit {
		if _, err := g.SetCurrentView("config"); err != nil {
			return err
		}
	}

	labelW := 14
	fmt.Fprintf(v, "\x1b[90m%s\x1b[0m\n", path)
	fmt.Fprintln(v, "")
	rows := a.cfgRows()
	for i, r := range rows {
		prefix := "  "
		if i == a.cfgCursor {
			prefix = "› "
		}
		val := r.get(*a.cfgActive())
		if val == "" {
			val = "\x1b[90m(empty)\x1b[0m"
		}
		fmt.Fprintf(v, "%s\x1b[1m%s\x1b[0m%s%s\n", prefix, r.label,
			strings.Repeat(" ", labelW-utf8.RuneCountInString(r.label)), val)
	}

	help := " tab: scope"
	if a.inRepo && a.cfgScope == config.ProjectScope {
		help += "/global"
	}
	help += "   ↑/↓: row   a: add   d: del   e: edit   enter: save   q/esc: back"
	return a.layoutHelp(g, maxX, bodyBottom, maxY, help)
}

// splitGlobs parses space-separated globs from a config line, dropping blanks.
func splitGlobs(s string) []string {
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (a *App) layoutHelp(g *gocui.Gui, maxX, bodyBottom, maxY int, help string) error {
	if err := a.setView(g, "help", 0, bodyBottom, maxX-1, maxY-1); err != nil {
		return err
	}
	v, _ := g.View("help")
	v.Visible = true
	v.Frame = false
	v.Clear()
	fmt.Fprintln(v, "\x1b[90m"+help+"\x1b[0m")
	if a.status != "" {
		fmt.Fprintln(v, "\x1b[31m "+a.status+"\x1b[0m")
	}
	return nil
}

func (a *App) seedPrompt(p *gocui.View) {
	if a.promptSeeded {
		return
	}
	p.Clear()
	fmt.Fprint(p, a.promptSeed)
	_ = p.SetCursor(utf8.RuneCountInString(a.promptSeed), 0)
	a.promptSeeded = true
}

func (a *App) openPrompt(seed string) {
	a.promptSeed = seed
	a.promptSeeded = false
}

func (a *App) listHelp() string {
	h := " enter: switch   ctrl-n: new   ctrl-x: kill   p: preview   ?: help   c: config"
	if a.inRepo {
		h += "   ctrl-w: worktree"
	}
	return h
}

func (a *App) nameColWidth(avail int) int {
	col := 0
	for _, s := range a.sessionInventory.Sessions {
		if l := utf8.RuneCountInString(s.Name); l > col {
			col = l
		}
	}
	if cap := avail - 14; cap > 0 && col > cap {
		col = cap
	}
	return col
}

func badge(st agent.State) string {
	switch st {
	case agent.Waiting:
		return "\x1b[33m● waiting\x1b[0m"
	case agent.Working:
		return "\x1b[34m◐ working\x1b[0m"
	case agent.Plan:
		return "\x1b[35m⏸ plan\x1b[0m"
	default:
		return "\x1b[90m· idle\x1b[0m"
	}
}

// Physical viewport sizes for agent TUIs (source pane rows, before compact).
const (
	agentTopRows    = 14 // header / welcome / model / path
	agentBottomRows = 16 // alerts + prompt + footer
)

// Markers that mean "this pane is an agent TUI" (matched on full plain text).
var agentPaneMarkers = []string{
	"? for shortcuts",
	"esc interrupt",
	"esc to interrupt",
	"ctrl+p commands",
	"shift+tab to cycle",
	"claude code",
	"opencode",
	"manual mode",
	"ctrl+c to stop",
}

// fitPreview turns a full-width pane capture into lines that fit a small
// in-terminal preview panel (ANSI kept). Agent TUIs are sampled as a mini
// viewport: top of the screen (header) + bottom (prompt/footer), so middle
// transcript dump is skipped and idle Claude isn't reduced to one status line.
func fitPreview(raw string, width, height int) []string {
	if width < 1 || height < 1 || raw == "" {
		return nil
	}
	rows := strings.Split(raw, "\n")
	// Drop trailing empty from final newline.
	if len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}

	var selected []string
	if isAgentPane(raw) {
		selected = fitAgentViewport(rows, height)
	} else {
		selected = tailRows(rows, height)
	}
	for i, ln := range selected {
		selected[i] = truncateANSI(ln, width)
	}
	return selected
}

func isAgentPane(raw string) bool {
	plain := strings.ToLower(stripANSI(raw))
	for _, m := range agentPaneMarkers {
		if strings.Contains(plain, m) {
			return true
		}
	}
	return false
}

// fitAgentViewport: top physical rows (header/welcome/model/path) + bottom
// physical rows (prompt/footer), with the middle transcript dropped. Rows are
// kept verbatim — only the width truncation happens later in fitPreview.
func fitAgentViewport(rows []string, height int) []string {
	if len(rows) == 0 {
		return nil
	}
	topN := agentTopRows
	botN := agentBottomRows
	if topN+botN > len(rows) {
		// Small pane: just use everything.
		return tailRows(rows, height)
	}

	top := append([]string{}, rows[:topN]...)
	bot := append([]string{}, rows[len(rows)-botN:]...)

	out := append(top, bot...)
	out = dedupeAdjacent(out)
	if len(out) > height {
		// Prefer bottom if over budget: header can shrink first.
		keepBot := len(bot)
		if keepBot > height {
			keepBot = height
			bot = bot[len(bot)-keepBot:]
			return bot
		}
		keepTop := height - keepBot
		if keepTop > len(top) {
			keepTop = len(top)
		}
		if keepTop < 0 {
			keepTop = 0
		}
		out = append(top[len(top)-keepTop:], bot...)
	}
	return out
}

// tailRows returns the last height rows of a captured pane verbatim (ANSI
// preserved, only truncated to fit the panel width by the caller). The cursor
// sits at the bottom of a pane, so this is the slice the user wants to see.
func tailRows(rows []string, height int) []string {
	if height > 0 && len(rows) > height {
		rows = rows[len(rows)-height:]
	}
	return rows
}

func dedupeAdjacent(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := []string{lines[0]}
	for i := 1; i < len(lines); i++ {
		if stripANSI(lines[i]) == stripANSI(out[len(out)-1]) {
			continue
		}
		out = append(out, lines[i])
	}
	return out
}

// truncateANSI cuts s to at most width display cells, keeping escape sequences
// that appear before the cut and appending a reset so colors don't leak.
func truncateANSI(s string, width int) string {
	if width < 1 {
		return ""
	}
	if runewidth.StringWidth(stripANSI(s)) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(r)
		if w+rw > width {
			break
		}
		b.WriteString(s[i : i+size])
		w += rw
		i += size
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// escapeLen returns the byte length of an ANSI escape at the start of s, or 0.
func escapeLen(s string) int {
	if len(s) < 2 || s[0] != '\x1b' {
		return 0
	}
	switch s[1] {
	case '[': // CSI
		i := 2
		for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
			i++
		}
		if i < len(s) {
			return i + 1
		}
		return len(s)
	case ']': // OSC
		i := 2
		for i < len(s) {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return len(s)
	default:
		// Other ESC sequences (e.g. ESC ( B) — take ESC + next byte if present.
		if len(s) >= 2 {
			return 2
		}
		return 1
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if n := escapeLen(s[i:]); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ── keys ────────────────────────────────────────────────────────────

func (a *App) bindKeys(g *gocui.Gui) error {
	// Global: never need to be typed as characters.
	for _, key := range []interface{}{gocui.KeyCtrlC, gocui.KeyEsc} {
		if err := g.SetKeybinding("", key, gocui.ModNone, a.onQuitOrBack); err != nil {
			return err
		}
	}
	for _, pair := range []struct {
		key interface{}
		fn  func(*gocui.Gui, *gocui.View) error
	}{
		{gocui.KeyArrowUp, a.onUp},
		{gocui.KeyArrowDown, a.onDown},
		{gocui.KeyCtrlP, a.onUp},
		{gocui.KeyCtrlN, a.onCtrlN},
		{gocui.KeyEnter, a.onEnter},
		{gocui.KeyCtrlW, a.onCtrlW},
		{gocui.KeyCtrlX, a.onCtrlX},
	} {
		if err := g.SetKeybinding("", pair.key, gocui.ModNone, pair.fn); err != nil {
			return err
		}
	}

	// Letters only on non-editable views so prompts can type j/k/y/n/q/p.
	for _, view := range []string{"list", "confirm"} {
		for _, pair := range []struct {
			key interface{}
			fn  func(*gocui.Gui, *gocui.View) error
		}{
			{'q', a.onQuitOrBack},
			{'k', a.onUp},
			{'j', a.onDown},
			{'y', a.onYes},
			{'Y', a.onYes},
			{'n', a.onNo},
			{'N', a.onNo},
			{'p', a.onTogglePreview},
			{'?', a.onToggleHelp},
			{'c', a.onToggleConfig},
		} {
			if err := g.SetKeybinding(view, pair.key, gocui.ModNone, pair.fn); err != nil {
				return err
			}
		}
	}

	// Close the help overlay from within it. esc is handled globally by
	// onQuitOrBack, which checks modeHelp first.
	for _, pair := range []struct {
		key interface{}
		fn  func(*gocui.Gui, *gocui.View) error
	}{
		{'?', a.onCloseHelp},
		{'q', a.onCloseHelp},
	} {
		if err := g.SetKeybinding("help", pair.key, gocui.ModNone, pair.fn); err != nil {
			return err
		}
	}

	// Config view navigation + editing. esc/q handled globally by onQuitOrBack
	// (modeConfig branch).
	for _, pair := range []struct {
		key interface{}
		fn  func(*gocui.Gui, *gocui.View) error
	}{
		{gocui.KeyTab, a.onCfgTab},
		{gocui.KeyArrowUp, a.onCfgUp},
		{gocui.KeyArrowDown, a.onCfgDown},
		{'k', a.onCfgUp},
		{'j', a.onCfgDown},
		{'a', a.onCfgAdd},
		{'d', a.onCfgDel},
		{'e', a.onCfgEdit},
		{gocui.KeyEnter, a.onCfgSave},
	} {
		if err := g.SetKeybinding("config", pair.key, gocui.ModNone, pair.fn); err != nil {
			return err
		}
	}

	// While editing a config line, the cfgedit prompt commits to the list on
	// enter/esc/c. A dedicated view keeps it from colliding with the shared
	// "prompt" used by new-session / branch-pick modes.
	for _, pair := range []struct {
		key interface{}
		fn  func(*gocui.Gui, *gocui.View) error
	}{
		{gocui.KeyEnter, a.onCfgCommit},
		{gocui.KeyEsc, a.onCfgCommit},
		{'c', a.onCfgCommit},
	} {
		if err := g.SetKeybinding("cfgedit", pair.key, gocui.ModNone, pair.fn); err != nil {
			return err
		}
	}

	// Mini-fzf file picker. View-specific keybindings take precedence over
	// globals (so ↑/↓, j/k, tab, enter, esc, ctrl-a, ctrl-u go to picker
	// handlers, not onUp/onDown/onEnter/onQuitOrBack). The view's Editor
	// only sees characters and backspace.
	for _, pair := range []struct {
		key interface{}
		fn  func(*gocui.Gui, *gocui.View) error
	}{
		{gocui.KeyArrowUp, a.onCfgPickUp},
		{gocui.KeyArrowDown, a.onCfgPickDown},
		{gocui.KeyTab, a.onCfgPickToggle},
		{gocui.KeyEnter, a.onCfgPickCommit},
		{gocui.KeyEsc, a.onCfgPickClose},
		{gocui.KeyCtrlA, a.onCfgPickMarkAll},
		{gocui.KeyCtrlU, a.onCfgPickClearMarks},
		{'k', a.onCfgPickUp},
		{'j', a.onCfgPickDown},
	} {
		if err := g.SetKeybinding("cfgpick", pair.key, gocui.ModNone, pair.fn); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) onTogglePreview(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeList {
		return nil
	}
	a.previewOn = !a.previewOn
	if a.previewOn {
		a.invalidatePreview()
	}
	return nil
}

func (a *App) onToggleHelp(g *gocui.Gui, v *gocui.View) error {
	if a.mode == modeHelp {
		a.mode = modeList
		return nil
	}
	a.mode = modeHelp
	return nil
}

func (a *App) onCloseHelp(g *gocui.Gui, v *gocui.View) error {
	a.mode = modeList
	return nil
}

// ── config view ─────────────────────────────────────────────────────

// cfgRows describes the editable lines, independent of the active scope.
func (a *App) cfgRows() []configRow {
	return []configRow{
		{
			label:    "worktree_dir",
			get:      func(v config.Values) string { return v.WorktreeDir },
			set:      func(v *config.Values, val string) { v.WorktreeDir = val },
			editable: true,
		},
		{
			label: "files.copy",
			get:   func(v config.Values) string { return strings.Join(v.Copy, "  ") },
			set:   func(v *config.Values, val string) { v.Copy = splitGlobs(val) },
		},
		{
			label: "files.symlink",
			get:   func(v config.Values) string { return strings.Join(v.Symlink, "  ") },
			set:   func(v *config.Values, val string) { v.Symlink = splitGlobs(val) },
		},
	}
}

func (a *App) cfgActive() *config.Values {
	if a.cfgScope == config.ProjectScope {
		return &a.cfgProject
	}
	return &a.cfgGlobal
}

func (a *App) cfgPath() string {
	if a.cfgScope == config.ProjectScope {
		return config.ProjectPath(a.repoRoot)
	}
	return mustGlobalPath()
}

func (a *App) onToggleConfig(g *gocui.Gui, v *gocui.View) error {
	if a.mode == modeConfig {
		a.mode = modeList
		return nil
	}
	if a.inRepo {
		a.cfgScope = config.ProjectScope
	} else {
		a.cfgScope = config.GlobalScope
	}
	a.cfgCursor = 0
	a.cfgEntry = 0
	a.status = ""
	a.cfgEditSeeded = false
	a.mode = modeConfig
	return nil
}

func (a *App) onCfgTab(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig || !a.inRepo {
		return nil
	}
	if a.cfgScope == config.GlobalScope {
		a.cfgScope = config.ProjectScope
	} else {
		a.cfgScope = config.GlobalScope
	}
	a.cfgCursor = 0
	a.status = ""
	return nil
}

func (a *App) onCfgUp(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig {
		return nil
	}
	if a.cfgCursor > 0 {
		a.cfgCursor--
	}
	return nil
}

func (a *App) onCfgDown(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig {
		return nil
	}
	if a.cfgCursor < len(a.cfgRows())-1 {
		a.cfgCursor++
	}
	return nil
}

// onCfgEdit opens the current row in the prompt for editing (dir or globs).
func (a *App) onCfgEdit(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig {
		return nil
	}
	row := a.cfgRows()[a.cfgCursor]
	a.cfgEditSeed = row.get(*a.cfgActive())
	a.cfgEditSeeded = false
	a.mode = modeConfigEdit
	return nil
}

func (a *App) seedCfgEdit(p *gocui.View) {
	if a.cfgEditSeeded {
		return
	}
	p.Clear()
	fmt.Fprint(p, a.cfgEditSeed)
	_ = p.SetCursor(utf8.RuneCountInString(a.cfgEditSeed), 0)
	a.cfgEditSeeded = true
}

// onCfgAdd dispatches by row: worktree_dir opens a text editor; copy/symlink
// rows open the mini-fzf file picker (when in a repo) or fall back to the text
// editor (global config / no repo).
func (a *App) onCfgAdd(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig {
		return nil
	}
	switch a.cfgCursor {
	case 1, 2:
		if a.inRepo && a.cfgScope == config.ProjectScope {
			a.cfgPickTarget = a.cfgCursor
			a.cfgPickQuery = ""
			a.cfgPickCursor = 0
			a.cfgPickMarked = map[int]bool{}
			if !a.cfgPickLoaded {
				a.cfgPickFiles, a.cfgPickTruncated = loadRepoFiles(a.repoRoot)
				a.cfgPickLoaded = true
			}
			a.cfgPickFiltered = pickFilter(a.cfgPickFiles, "")
			a.mode = modeCfgPick
			return nil
		}
	}
	// Fallback: text editor (worktree_dir, or copy/symlink in global config).
	vv := a.cfgActive()
	switch a.cfgCursor {
	case 1:
		vv.Copy = append(vv.Copy, "")
		a.cfgEntry = len(vv.Copy) - 1
	case 2:
		vv.Symlink = append(vv.Symlink, "")
		a.cfgEntry = len(vv.Symlink) - 1
	default:
		return a.onCfgEdit(g, v)
	}
	a.cfgEditSeed = ""
	a.cfgEditSeeded = false
	a.mode = modeConfigEdit
	return nil
}

// onCfgDel removes the highlighted entry from the focused list row.
func (a *App) onCfgDel(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig {
		return nil
	}
	vv := a.cfgActive()
	switch a.cfgCursor {
	case 1:
		if a.cfgEntry >= 0 && a.cfgEntry < len(vv.Copy) {
			vv.Copy = append(vv.Copy[:a.cfgEntry], vv.Copy[a.cfgEntry+1:]...)
			if a.cfgEntry >= len(vv.Copy) {
				a.cfgEntry = len(vv.Copy) - 1
			}
		}
	case 2:
		if a.cfgEntry >= 0 && a.cfgEntry < len(vv.Symlink) {
			vv.Symlink = append(vv.Symlink[:a.cfgEntry], vv.Symlink[a.cfgEntry+1:]...)
			if a.cfgEntry >= len(vv.Symlink) {
				a.cfgEntry = len(vv.Symlink) - 1
			}
		}
	}
	return nil
}

// onCfgCommit applies the prompt text to the current row and returns to the
// config list. For list rows, the text is space-separated globs.
func (a *App) onCfgCommit(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfigEdit {
		return nil
	}
	buf := strings.TrimSpace(cfgEditBuffer(g))
	row := a.cfgRows()[a.cfgCursor]
	row.set(a.cfgActive(), buf)
	a.mode = modeConfig
	a.status = ""
	a.cfgEditSeed = ""
	a.cfgEditSeeded = false
	return nil
}

func cfgEditBuffer(g *gocui.Gui) string {
	v, err := g.View("cfgedit")
	if err != nil {
		return ""
	}
	return strings.TrimRight(v.Buffer(), "\n")
}

// ── mini-fzf file picker ───────────────────────────────────────────

const cfgPickMaxFiles = 5000

// loadRepoFiles walks repoRoot and returns paths relative to it, skipping
// .git/. Capped at cfgPickMaxFiles.
func loadRepoFiles(repoRoot string) ([]string, bool) {
	out := make([]string, 0, 256)
	truncated := false
	_ = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == repoRoot {
				return nil
			}
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= cfgPickMaxFiles {
			truncated = true
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, truncated
}

// pickFilter returns indices into files that fuzzy-match query, sorted by
// descending score. Empty query → all indices in original order.
func pickFilter(files []string, query string) []int {
	if query == "" {
		out := make([]int, len(files))
		for i := range files {
			out[i] = i
		}
		return out
	}
	type cand struct {
		idx   int
		score int
	}
	cands := make([]cand, 0, len(files))
	for i, f := range files {
		if s, ok := fuzzyMatch(query, f); ok {
			cands = append(cands, cand{i, s})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].idx < cands[j].idx
	})
	out := make([]int, len(cands))
	for i, c := range cands {
		out[i] = c.idx
	}
	return out
}

// fuzzyMatch returns a score if every rune of query appears in path in order
// (case-insensitive). Higher = better. 0 / false means no match.
func fuzzyMatch(query, path string) (int, bool) {
	ql := strings.ToLower(query)
	pl := strings.ToLower(path)
	score := 0
	pi := 0
	for i := 0; i < len(ql); i++ {
		q := ql[i]
		found := -1
		for j := pi; j < len(pl); j++ {
			if pl[j] == q {
				found = j
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		score += 1
		if found == 0 {
			score += 50
		} else if pl[found-1] == '/' {
			score += 20
		}
		pi = found + 1
	}
	// Shorter paths win slightly.
	score -= len(path) / 50
	return score, true
}

// appendUniqueGlob appends g to list if no entry equals g.
func appendUniqueGlob(list []string, g string) []string {
	for _, e := range list {
		if e == g {
			return list
		}
	}
	return append(list, g)
}

// cfgPickRefresh reapplies the fuzzy filter and clamps the cursor.
func (a *App) cfgPickRefresh() {
	a.cfgPickFiltered = pickFilter(a.cfgPickFiles, a.cfgPickQuery)
	if a.cfgPickCursor >= len(a.cfgPickFiltered) {
		a.cfgPickCursor = len(a.cfgPickFiltered) - 1
	}
	if a.cfgPickCursor < 0 {
		a.cfgPickCursor = 0
	}
}

// cfgPickAddFiles commits marked (or current) files to the target row.
func (a *App) cfgPickAddFiles() int {
	if len(a.cfgPickMarked) == 0 {
		if a.cfgPickCursor < 0 || a.cfgPickCursor >= len(a.cfgPickFiltered) {
			return 0
		}
		a.cfgPickMarked[a.cfgPickFiltered[a.cfgPickCursor]] = true
	}
	added := 0
	for idx := range a.cfgPickMarked {
		if idx < 0 || idx >= len(a.cfgPickFiles) {
			continue
		}
		entry := a.cfgPickFiles[idx]
		vv := a.cfgActive()
		switch a.cfgPickTarget {
		case 1:
			vv.Copy = appendUniqueGlob(vv.Copy, entry)
		case 2:
			vv.Symlink = appendUniqueGlob(vv.Symlink, entry)
		}
		added++
	}
	a.cfgPickMarked = map[int]bool{}
	a.cfgPickQuery = ""
	a.cfgPickRefresh()
	return added
}

// onCfgPickUp / onCfgPickDown move the file cursor.
func (a *App) onCfgPickUp(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	if a.cfgPickCursor > 0 {
		a.cfgPickCursor--
	}
	return nil
}

func (a *App) onCfgPickDown(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	if a.cfgPickCursor < len(a.cfgPickFiltered)-1 {
		a.cfgPickCursor++
	}
	return nil
}

// onCfgPickToggle marks/unmarks the file under the cursor.
func (a *App) onCfgPickToggle(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	if a.cfgPickCursor < 0 || a.cfgPickCursor >= len(a.cfgPickFiltered) {
		return nil
	}
	idx := a.cfgPickFiltered[a.cfgPickCursor]
	if a.cfgPickMarked[idx] {
		delete(a.cfgPickMarked, idx)
	} else {
		a.cfgPickMarked[idx] = true
	}
	return nil
}

// onCfgPickCommit adds marked (or current) files to the target row and resets
// the picker (clears query, marks, file list) so the user can add more.
func (a *App) onCfgPickCommit(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	if n := a.cfgPickAddFiles(); n > 0 {
		a.cfgPickLastAdded = n
		a.cfgPickLastAddedAt = time.Now()
	}
	// Clear the query view's buffer so the model (a.cfgPickQuery="") matches
	// what's on screen.
	if qv, err := g.View("cfgpick"); err == nil {
		qv.Clear()
		_ = qv.SetCursor(0, 0)
	}
	return nil
}

// onCfgPickClose returns to the config view.
func (a *App) onCfgPickClose(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	a.mode = modeConfig
	return nil
}

func (a *App) onCfgPickMarkAll(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	for _, idx := range a.cfgPickFiltered {
		a.cfgPickMarked[idx] = true
	}
	return nil
}

func (a *App) onCfgPickClearMarks(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeCfgPick {
		return nil
	}
	a.cfgPickMarked = map[int]bool{}
	return nil
}

// layoutCfgPick draws the picker as two views:
//   - cfgpick:      single-line editable query, uses the default editor so
//     the visible cursor tracks typed text.
//   - cfgpicklist:  read-only file list with gocui's highlight on the
//     current row (cursor position = current file).
func (a *App) layoutCfgPick(g *gocui.Gui, maxX, top, bodyBottom, maxY int) error {
	// Query view (single line, default editor for cursor management).
	if err := a.setView(g, "cfgpick", 0, top, maxX-1, top); err != nil {
		return err
	}
	qv, _ := g.View("cfgpick")
	qv.Visible = true
	qv.Editable = true
	qv.Editor = gocui.DefaultEditor
	qv.Frame = true
	qv.Title = " search "
	// Sync a.cfgPickQuery from the view buffer (the default editor writes
	// here). On first render the buffer is empty and the field is already "".
	if buf := strings.TrimRight(qv.Buffer(), "\n"); buf != a.cfgPickQuery {
		a.cfgPickQuery = buf
		a.cfgPickCursor = 0
		a.cfgPickRefresh()
	}

	// List view (multi-line, read-only, highlighted).
	if err := a.setView(g, "cfgpicklist", 0, top+1, maxX-1, bodyBottom); err != nil {
		return err
	}
	lv, _ := g.View("cfgpicklist")
	lv.Visible = true
	lv.Editable = false
	lv.Frame = true
	lv.Highlight = true
	lv.SelBgColor = gocui.ColorBlue
	lv.SelFgColor = gocui.ColorWhite | gocui.AttrBold
	target := "files.copy"
	if a.cfgPickTarget == 2 {
		target = "files.symlink"
	}
	lv.Title = " pick file for " + target + " "
	lv.Clear()

	listH := bodyBottom - (top + 1) - 1
	if listH < 1 {
		listH = 1
	}
	start := 0
	if a.cfgPickCursor >= listH {
		start = a.cfgPickCursor - listH + 1
	}
	end := start + listH
	if end > len(a.cfgPickFiltered) {
		end = len(a.cfgPickFiltered)
	}
	for i := start; i < end; i++ {
		idx := a.cfgPickFiltered[i]
		marker := "  "
		if a.cfgPickMarked[idx] {
			marker = "✓ "
		}
		fmt.Fprintf(lv, "%s%s\n", marker, a.cfgPickFiles[idx])
	}
	// Cursor on the list view = which row is highlighted.
	if a.cfgPickCursor >= start && a.cfgPickCursor < end {
		_ = lv.SetCursor(0, a.cfgPickCursor-start)
	}

	if _, err := g.SetCurrentView("cfgpick"); err != nil {
		return err
	}

	footer := fmt.Sprintf(" %d/%d files · %d marked",
		len(a.cfgPickFiltered), len(a.cfgPickFiles), len(a.cfgPickMarked))
	if a.cfgPickTruncated {
		footer += " (truncated)"
	}
	if a.cfgPickLastAdded > 0 && time.Since(a.cfgPickLastAddedAt) < 2*time.Second {
		footer += fmt.Sprintf("   \x1b[32m✓ added %d file(s)\x1b[0m", a.cfgPickLastAdded)
	} else if a.cfgPickLastAdded > 0 {
		a.cfgPickLastAdded = 0
	}
	footer += "   tab: multi   ↑/↓ j/k: nav   enter: add   ctrl-a/u: marks   esc: back"
	return a.layoutHelp(g, maxX, bodyBottom, maxY, footer)
}

func (a *App) onCfgSave(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfig && a.mode != modeConfigEdit {
		return nil
	}
	base, err := config.LoadScope(a.cfgPath())
	if err != nil {
		a.status = "Failed to read config: " + err.Error()
		return nil
	}
	saved := a.cfgActive().ToConfig(base)
	if err := config.SaveScope(a.cfgPath(), saved); err != nil {
		a.status = "Failed to save config: " + err.Error()
		return nil
	}
	a.status = "Saved " + a.cfgPath()
	return nil
}

func (a *App) onQuitOrBack(g *gocui.Gui, v *gocui.View) error {
	switch a.mode {
	case modeList:
		return gocui.ErrQuit
	case modeNewSession, modeBranchPick, modeConfirm:
		a.mode = modeList
		a.status = ""
		return nil
	case modeHelp:
		a.mode = modeList
		return nil
	case modeConfig, modeConfigEdit:
		a.mode = modeList
		a.status = ""
		return nil
	case modeCfgPick:
		a.mode = modeConfig
		return nil
	}
	return gocui.ErrQuit
}

func (a *App) onUp(g *gocui.Gui, v *gocui.View) error {
	switch a.mode {
	case modeList:
		if a.cursor > 0 {
			a.cursor--
			a.refreshPreview(false)
		}
	case modeBranchPick:
		if a.bcursor > 0 {
			a.bcursor--
		}
	}
	return nil
}

func (a *App) onDown(g *gocui.Gui, v *gocui.View) error {
	switch a.mode {
	case modeList:
		if a.cursor < len(a.sessionInventory.Sessions)-1 {
			a.cursor++
			a.refreshPreview(false)
		}
	case modeBranchPick:
		if a.bcursor < len(a.filtered)-1 {
			a.bcursor++
		}
	}
	return nil
}

func (a *App) onCtrlN(g *gocui.Gui, v *gocui.View) error {
	switch a.mode {
	case modeList:
		a.mode = modeNewSession
		a.status = ""
		a.openPrompt(filepath.Base(a.paneDir))
		_ = g.DeleteView("prompt")
	case modeBranchPick:
		return a.onDown(g, v)
	}
	return nil
}

func (a *App) onCtrlW(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeList || !a.inRepo {
		return nil
	}
	a.mode = modeBranchPick
	a.branches = git.Branches(a.repoRoot)
	a.filtered = a.branches
	a.bcursor = 0
	a.status = ""
	a.openPrompt("")
	_ = g.DeleteView("prompt")
	return nil
}

func (a *App) onCtrlX(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeList {
		return nil
	}
	return a.startKill()
}

func (a *App) onEnter(g *gocui.Gui, v *gocui.View) error {
	switch a.mode {
	case modeList:
		if len(a.sessionInventory.Sessions) > 0 {
			a.Target = a.sessionInventory.Sessions[a.cursor].Name
			return gocui.ErrQuit
		}
	case modeNewSession:
		return a.createPlainSession(g)
	case modeBranchPick:
		return a.createWorktreeSession(g)
	}
	return nil
}

func (a *App) onYes(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfirm {
		return nil
	}
	a.doKill(a.pendingKill, true, a.pendingWtRoot)
	return nil
}

func (a *App) onNo(g *gocui.Gui, v *gocui.View) error {
	if a.mode != modeConfirm {
		return nil
	}
	a.doKill(a.pendingKill, false, "")
	return nil
}

// ── actions ─────────────────────────────────────────────────────────

func (a *App) createPlainSession(g *gocui.Gui) error {
	name := strings.TrimSpace(promptBuffer(g))
	if name == "" {
		a.mode = modeList
		return nil
	}
	if err := tmux.NewSession(name, a.paneDir); err != nil {
		a.status = "Failed to create session: " + err.Error()
		a.mode = modeList
		return nil
	}
	a.Target = name
	return gocui.ErrQuit
}

func (a *App) createWorktreeSession(g *gocui.Gui) error {
	branch := selectedWorktreeSource(promptBuffer(g), a.filtered, a.bcursor)
	if branch == "" {
		a.mode = modeList
		return nil
	}

	cfg := config.Load(a.repoRoot)
	safe := strings.ReplaceAll(branch, "/", "-")
	wtPath := filepath.Join(a.repoRoot, cfg.WorktreeDir, safe)
	sessionName := filepath.Base(a.repoRoot) + "-" + safe

	_ = git.EnsureWorktreesIgnored(a.repoRoot, cfg.WorktreeDir)

	resolved, err := git.EnsureWorktree(a.repoRoot, branch, wtPath)
	if err != nil {
		a.status = "git worktree add failed: " + err.Error()
		a.mode = modeList
		return nil
	}
	if err := cfg.ApplyFiles(a.repoRoot, resolved); err != nil {
		a.status = "worktree created, but copying files failed: " + err.Error()
	}
	if err := tmux.NewSession(sessionName, resolved); err != nil {
		a.status = "Failed to create session: " + err.Error()
		a.mode = modeList
		return nil
	}
	a.Target = sessionName
	return gocui.ErrQuit
}

// selectedWorktreeSource gives explicit typed input precedence over the picker.
// An empty prompt means the highlighted suggestion is the selected source.
func selectedWorktreeSource(typed string, choices []string, cursor int) string {
	if typed = strings.TrimSpace(typed); typed != "" {
		return typed
	}
	if cursor >= 0 && cursor < len(choices) {
		return choices[cursor]
	}
	return ""
}

// sessionInventorySummary renders optional context compactly so ordinary tmux
// sessions remain visible even when Git metadata is unavailable.
func sessionInventorySummary(row inventory.Session, now time.Time) string {
	var parts []string
	if row.HasGitContext {
		if row.Branch != "" {
			parts = append(parts, row.Branch)
		}
		if row.Dirty {
			parts = append(parts, "dirty")
		} else {
			parts = append(parts, "clean")
		}
		if row.LinkedWorktree {
			parts = append(parts, "worktree "+row.WorktreePath)
		} else {
			parts = append(parts, "repo "+row.WorktreePath)
		}
	}
	if !row.Activity.IsZero() && !now.Before(row.Activity) {
		elapsed := now.Sub(row.Activity)
		if elapsed < time.Minute {
			parts = append(parts, "active now")
		} else {
			parts = append(parts, "active "+compactDuration(elapsed)+" ago")
		}
	}
	return strings.Join(parts, " · ")
}

func compactDuration(duration time.Duration) string {
	if duration < time.Minute {
		return "now"
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd", int(duration.Hours()/24))
}

func (a *App) startKill() error {
	if len(a.sessionInventory.Sessions) == 0 {
		return nil
	}
	sess := a.sessionInventory.Sessions[a.cursor]
	if sess.Name == a.attached {
		a.status = "Can't kill the session you're attached to — switch away first."
		return nil
	}
	if sess.LinkedWorktree {
		a.pendingKill = sess
		a.pendingWtRoot = sess.WorktreePath
		a.confirmMsg = "Remove worktree '" + sess.WorktreePath + "'? (session is killed either way)"
		a.mode = modeConfirm
		return nil
	}
	a.doKill(sess, false, "")
	return nil
}

func (a *App) doKill(sess inventory.Session, removeWt bool, wtRoot string) {
	if removeWt {
		if err := git.RemoveWorktree(wtRoot, wtRoot); err != nil {
			a.status = "Worktree has changes — left on disk; remove manually with --force."
		}
	}
	if err := tmux.KillSession(sess.Name); err != nil {
		a.status = "Failed to kill '" + sess.Name + "'."
	}
	a.sessionInventory = buildInventory(tmux.ListSessions(), a.markers)
	if a.cursor >= len(a.sessionInventory.Sessions) {
		a.cursor = max(0, len(a.sessionInventory.Sessions)-1)
	}
	a.invalidatePreview()
	a.mode = modeList
}

func buildInventory(sessions []tmux.Session, markers agent.Markers) inventory.Snapshot {
	return inventory.Build(sessions, git.ContextForDir, func(session tmux.Session) agent.State {
		return agent.Detect(tmux.CapturePlain(session.Name), markers)
	})
}

func (a *App) branchEditor(v *gocui.View, key gocui.Key, ch rune, mod gocui.Modifier) {
	if key == gocui.KeyEnter {
		return
	}
	// Single-line: ignore vertical moves / newline.
	if key == gocui.KeyArrowUp || key == gocui.KeyArrowDown {
		return
	}
	gocui.DefaultEditor.Edit(v, key, ch, mod)
	query := strings.TrimRight(v.Buffer(), "\n")
	a.filtered = filter(a.branches, query)
	if a.bcursor >= len(a.filtered) {
		a.bcursor = max(0, len(a.filtered)-1)
	}
}

func promptBuffer(g *gocui.Gui) string {
	v, err := g.View("prompt")
	if err != nil {
		return ""
	}
	return strings.TrimRight(v.Buffer(), "\n")
}

// mustGlobalPath returns the global config path, falling back to the default
// layout if the home dir can't be resolved.
func mustGlobalPath() string {
	p, err := config.GlobalPath()
	if err != nil {
		return filepath.Join(".config", "sx", "config.yaml")
	}
	return p
}

func filter(items []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	var out []string
	for _, it := range items {
		if strings.Contains(strings.ToLower(it), query) {
			out = append(out, it)
		}
	}
	return out
}
