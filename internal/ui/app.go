// Package ui implements the sx TUI (the `sx menu` popup body) with gocui.
package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/m7medVision/sx/internal/agent"
	"github.com/m7medVision/sx/internal/config"
	"github.com/m7medVision/sx/internal/git"
	"github.com/m7medVision/sx/internal/tmux"
	"github.com/m7medVision/sx/internal/update"

	"github.com/awesome-gocui/gocui"
)

type mode int

const (
	modeList mode = iota
	modeNewSession
	modeBranchPick
	modeConfirm
)

// App holds TUI state. Target is the session to switch to after the popup closes.
type App struct {
	mode mode

	paneDir  string
	inRepo   bool
	repoRoot string
	attached string

	sessions []tmux.Session
	cursor   int

	markers agent.Markers
	states  map[string]agent.State

	// promptSeed is written into the prompt view once after a mode switch.
	promptSeed   string
	promptSeeded bool

	branches []string
	filtered []string
	bcursor  int

	confirmMsg    string
	pendingKill   tmux.Session
	pendingWtRoot string

	status string
	Target string

	version      string
	updateLatest string
	updateAvail  bool
}

// Run starts the TUI and returns the chosen session name (may be empty).
func Run(paneDir, version string) (string, error) {
	g, err := gocui.NewGui(gocui.OutputNormal, true)
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
	attached, cursor := resolveFocus(sessions, paneDir)
	return &App{
		mode:     modeList,
		paneDir:  paneDir,
		inRepo:   inRepo,
		repoRoot: repoRoot,
		attached: attached,
		sessions: sessions,
		cursor:   cursor,
		markers:  markers,
		states:   detectStates(sessions, markers),
		version:  version,
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

func detectStates(sessions []tmux.Session, markers agent.Markers) map[string]agent.State {
	states := make(map[string]agent.State, len(sessions))
	for _, s := range sessions {
		states[s.Name] = agent.Detect(tmux.CapturePlain(s.Name), markers)
	}
	return states
}

func (a *App) checkUpdate(g *gocui.Gui) {
	latest, newer := update.Check(a.version)
	g.Update(func(g *gocui.Gui) error {
		a.updateLatest = latest
		a.updateAvail = newer
		return nil
	})
}

// ── layout ──────────────────────────────────────────────────────────

func (a *App) layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	if maxX < 10 || maxY < 5 {
		return nil
	}

	for _, name := range []string{"banner", "list", "preview", "prompt", "branches", "confirm", "help"} {
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
	showPreview := maxX > 50
	listRight := maxX - 1
	if showPreview {
		listRight = maxX * 45 / 100
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

	if len(a.sessions) == 0 {
		fmt.Fprintln(list, "  (no sessions)")
	} else {
		nameCol := a.nameColWidth(listRight - 2)
		for i, s := range a.sessions {
			prefix := "  "
			if i == a.cursor {
				prefix = "› "
			}
			pad := nameCol - utf8.RuneCountInString(s.Name)
			if pad < 0 {
				pad = 0
			}
			line := prefix + s.Name + strings.Repeat(" ", pad) + "  " + badge(a.states[s.Name])
			if s.Name == a.attached {
				line += " \x1b[90m(attached)\x1b[0m"
			}
			fmt.Fprintln(list, line)
		}
		// Clear() resets the cursor; re-apply after writing lines.
		// gocui Highlight compares cy to *screen* y (after origin), not buffer y.
		_, h := list.Size()
		oy := 0
		if a.cursor >= h {
			oy = a.cursor - h + 1
		}
		_ = list.SetOrigin(0, oy)
		_ = list.SetCursorUnrestricted(0, a.cursor-oy)
	}
	if _, err := g.SetCurrentView("list"); err != nil {
		return err
	}

	if showPreview && len(a.sessions) > 0 {
		if err := a.setView(g, "preview", listRight, top, maxX-1, bodyBottom); err != nil {
			return err
		}
		pv, _ := g.View("preview")
		pv.Visible = true
		pv.Title = " preview "
		pv.Wrap = false
		pv.Clear()
		w, h := pv.Size()
		raw := tmux.CapturePane(a.sessions[a.cursor].Name)
		for i, ln := range strings.Split(raw, "\n") {
			if i >= h {
				break
			}
			fmt.Fprintln(pv, clipDisplay(ln, w))
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
	h := " enter: switch   ctrl-n: new   ctrl-x: kill"
	if a.inRepo {
		h += "   ctrl-w: worktree"
	}
	return h
}

func (a *App) nameColWidth(avail int) int {
	col := 0
	for _, s := range a.sessions {
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

// clipDisplay truncates s to at most width display cells.
func clipDisplay(s string, width int) string {
	if width < 1 {
		return ""
	}
	if displayWidth(s) <= width {
		return s
	}
	plain := stripANSI(s)
	r := []rune(plain)
	if len(r) <= width {
		return plain
	}
	return string(r[:width])
}

func displayWidth(s string) int {
	return utf8.RuneCountInString(stripANSI(s))
}

func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
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

	// Letters only on non-editable views so prompts can type j/k/y/n/q.
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
		} {
			if err := g.SetKeybinding(view, pair.key, gocui.ModNone, pair.fn); err != nil {
				return err
			}
		}
	}
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
	}
	return gocui.ErrQuit
}

func (a *App) onUp(g *gocui.Gui, v *gocui.View) error {
	switch a.mode {
	case modeList:
		if a.cursor > 0 {
			a.cursor--
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
		if a.cursor < len(a.sessions)-1 {
			a.cursor++
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
		if len(a.sessions) > 0 {
			a.Target = a.sessions[a.cursor].Name
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
	branch := strings.TrimSpace(promptBuffer(g))
	if len(a.filtered) > 0 {
		branch = a.filtered[a.bcursor]
	}
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

func (a *App) startKill() error {
	if len(a.sessions) == 0 {
		return nil
	}
	sess := a.sessions[a.cursor]
	if sess.Name == a.attached {
		a.status = "Can't kill the session you're attached to — switch away first."
		return nil
	}
	if root, linked := git.IsLinkedWorktree(sess.Path); linked {
		a.pendingKill = sess
		a.pendingWtRoot = root
		a.confirmMsg = "Remove worktree '" + root + "'? (session is killed either way)"
		a.mode = modeConfirm
		return nil
	}
	a.doKill(sess, false, "")
	return nil
}

func (a *App) doKill(sess tmux.Session, removeWt bool, wtRoot string) {
	if removeWt {
		if err := git.RemoveWorktree(wtRoot, wtRoot); err != nil {
			a.status = "Worktree has changes — left on disk; remove manually with --force."
		}
	}
	if err := tmux.KillSession(sess.Name); err != nil {
		a.status = "Failed to kill '" + sess.Name + "'."
	}
	a.sessions = tmux.ListSessions()
	a.states = detectStates(a.sessions, a.markers)
	if a.cursor >= len(a.sessions) {
		a.cursor = max(0, len(a.sessions)-1)
	}
	a.mode = modeList
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
