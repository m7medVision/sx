// Package ui implements the sx Bubble Tea TUI (the `sx menu` popup body).
package ui

import (
	"path/filepath"
	"strings"

	"github.com/m7medVision/sx/internal/config"
	"github.com/m7medVision/sx/internal/git"
	"github.com/m7medVision/sx/internal/tmux"
	"github.com/m7medVision/sx/internal/update"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type state int

const (
	stateList state = iota
	stateNewSession
	stateBranchPick
	stateConfirm
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	selStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	updateStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	previewStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
			BorderForeground(lipgloss.Color("8")).PaddingLeft(1)
)

// Model is the TUI state.
type Model struct {
	state         int
	width, height int

	paneDir  string
	inRepo   bool
	repoRoot string
	attached string

	sessions []tmux.Session
	cursor   int

	input textinput.Model // new-session name OR branch query

	branches []string
	filtered []string
	bcursor  int

	confirmMsg    string
	pendingKill   tmux.Session
	pendingWtRoot string

	status string // footer message (errors / warnings)
	Target string // session to switch to once the popup closes

	version      string // running build version (for the update check)
	updateLatest string // latest release tag, once known
	updateAvail  bool   // a newer release is available
}

// updateMsg carries the result of the async GitHub update check.
type updateMsg struct {
	latest string
	newer  bool
}

// checkUpdateCmd runs the update check off the render path; Bubble Tea executes
// it in a goroutine so the popup paints immediately regardless of the network.
func checkUpdateCmd(version string) tea.Cmd {
	return func() tea.Msg {
		latest, newer := update.Check(version)
		return updateMsg{latest: latest, newer: newer}
	}
}

// New builds the initial model rooted at the launching pane's directory.
func New(paneDir, version string) Model {
	ti := textinput.New()
	ti.Prompt = "› "

	repoRoot := ""
	inRepo := git.IsRepo(paneDir)
	if inRepo {
		repoRoot, _ = git.RepoRoot(paneDir)
	}

	return Model{
		state:    int(stateList),
		paneDir:  paneDir,
		inRepo:   inRepo,
		repoRoot: repoRoot,
		attached: tmux.ClientSession(),
		sessions: tmux.ListSessions(),
		input:    ti,
		version:  version,
	}
}

func (m Model) Init() tea.Cmd { return checkUpdateCmd(m.version) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = ws.Width, ws.Height
		return m, nil
	}
	if u, ok := msg.(updateMsg); ok {
		m.updateLatest = u.latest
		m.updateAvail = u.newer
		return m, nil
	}
	switch state(m.state) {
	case stateList:
		return m.updateList(msg)
	case stateNewSession:
		return m.updateNewSession(msg)
	case stateBranchPick:
		return m.updateBranchPick(msg)
	case stateConfirm:
		return m.updateConfirm(msg)
	}
	return m, nil
}

// ── Session list ────────────────────────────────────────────────────

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.sessions) > 0 {
			m.Target = m.sessions[m.cursor].Name
			return m, tea.Quit
		}
	case "ctrl+n":
		m.state = int(stateNewSession)
		m.input.Reset()
		m.input.SetValue(filepath.Base(m.paneDir))
		m.input.CursorEnd()
		m.input.Focus()
		m.status = ""
		return m, textinput.Blink
	case "ctrl+w":
		if m.inRepo {
			m.state = int(stateBranchPick)
			m.branches = git.Branches(m.repoRoot)
			m.filtered = m.branches
			m.bcursor = 0
			m.input.Reset()
			m.input.Focus()
			m.status = ""
			return m, textinput.Blink
		}
	case "ctrl+x":
		return m.startKill()
	}
	return m, nil
}

func (m Model) startKill() (tea.Model, tea.Cmd) {
	if len(m.sessions) == 0 {
		return m, nil
	}
	sess := m.sessions[m.cursor]
	if sess.Name == m.attached {
		m.status = "Can't kill the session you're attached to — switch away first."
		return m, nil
	}
	if root, linked := git.IsLinkedWorktree(sess.Path); linked {
		m.pendingKill = sess
		m.pendingWtRoot = root
		m.confirmMsg = "Remove worktree '" + root + "'? (session is killed either way)"
		m.state = int(stateConfirm)
		return m, nil
	}
	return m.doKill(sess, false, ""), nil
}

// ── New plain session ───────────────────────────────────────────────

func (m Model) updateNewSession(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.String() {
		case "esc", "ctrl+c":
			m.state = int(stateList)
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.input.Value())
			if name == "" {
				m.state = int(stateList)
				return m, nil
			}
			if err := tmux.NewSession(name, m.paneDir); err != nil {
				m.status = "Failed to create session: " + err.Error()
				m.state = int(stateList)
				return m, nil
			}
			m.Target = name
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// ── Branch picker / worktree session ────────────────────────────────

func (m Model) updateBranchPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "ctrl+c":
			m.state = int(stateList)
			return m, nil
		case "up", "ctrl+p":
			if m.bcursor > 0 {
				m.bcursor--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.bcursor < len(m.filtered)-1 {
				m.bcursor++
			}
			return m, nil
		case "enter":
			branch := strings.TrimSpace(m.input.Value())
			if len(m.filtered) > 0 {
				branch = m.filtered[m.bcursor]
			}
			if branch == "" {
				m.state = int(stateList)
				return m, nil
			}
			return m.createWorktreeSession(branch)
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filtered = filter(m.branches, m.input.Value())
	if m.bcursor >= len(m.filtered) {
		m.bcursor = max(0, len(m.filtered)-1)
	}
	return m, cmd
}

func (m Model) createWorktreeSession(branch string) (tea.Model, tea.Cmd) {
	cfg := config.Load(m.repoRoot)
	safe := strings.ReplaceAll(branch, "/", "-")
	wtPath := filepath.Join(m.repoRoot, cfg.WorktreeDir, safe)
	sessionName := filepath.Base(m.repoRoot) + "-" + safe

	_ = git.EnsureWorktreesIgnored(m.repoRoot, cfg.WorktreeDir)

	resolved, err := git.EnsureWorktree(m.repoRoot, branch, wtPath)
	if err != nil {
		m.status = "git worktree add failed: " + err.Error()
		m.state = int(stateList)
		return m, nil
	}
	if err := cfg.ApplyFiles(m.repoRoot, resolved); err != nil {
		m.status = "worktree created, but copying files failed: " + err.Error()
	}
	if err := tmux.NewSession(sessionName, resolved); err != nil {
		m.status = "Failed to create session: " + err.Error()
		m.state = int(stateList)
		return m, nil
	}
	m.Target = sessionName
	return m, tea.Quit
}

// ── Confirm (worktree removal on kill) ──────────────────────────────

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y":
		return m.doKill(m.pendingKill, true, m.pendingWtRoot), nil
	case "n", "N":
		return m.doKill(m.pendingKill, false, ""), nil
	case "esc", "ctrl+c", "q":
		m.state = int(stateList)
		return m, nil
	}
	return m, nil
}

// doKill kills a session, optionally removing its worktree first, then refreshes.
func (m Model) doKill(sess tmux.Session, removeWt bool, wtRoot string) Model {
	if removeWt {
		if err := git.RemoveWorktree(wtRoot, wtRoot); err != nil {
			m.status = "Worktree has changes — left on disk; remove manually with --force."
		}
	}
	if err := tmux.KillSession(sess.Name); err != nil {
		m.status = "Failed to kill '" + sess.Name + "'."
	}
	m.sessions = tmux.ListSessions()
	if m.cursor >= len(m.sessions) {
		m.cursor = max(0, len(m.sessions)-1)
	}
	m.state = int(stateList)
	return m
}

// ── Views ───────────────────────────────────────────────────────────

func (m Model) View() string {
	return m.updateBanner() + m.body()
}

// updateBanner is a one-line nudge shown across all states when a newer release
// is available; empty otherwise.
func (m Model) updateBanner() string {
	if !m.updateAvail {
		return ""
	}
	return updateStyle.Render(" ▲ update available: "+m.version+" → "+m.updateLatest+
		"   go install github.com/m7medVision/sx@latest") + "\n"
}

func (m Model) body() string {
	switch state(m.state) {
	case stateNewSession:
		return "\n" + titleStyle.Render(" New session ") + "\n\n" +
			m.input.View() + "\n\n" + dimStyle.Render(" enter: create   esc: back") + m.footer()
	case stateBranchPick:
		return m.branchView()
	case stateConfirm:
		return "\n" + titleStyle.Render(" Kill session ") + "\n\n " + m.confirmMsg + "\n\n" +
			dimStyle.Render(" y: remove worktree + kill   n: kill only   esc: cancel") + m.footer()
	default:
		return m.listView()
	}
}

func (m Model) listView() string {
	header := " enter: switch   ctrl-n: new   ctrl-x: kill"
	if m.inRepo {
		header += "   ctrl-w: worktree"
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(" tmux sessions ") + "\n\n")
	if len(m.sessions) == 0 {
		b.WriteString(dimStyle.Render("  (no sessions)") + "\n")
	}
	for i, s := range m.sessions {
		line := "  " + s.Name
		if s.Name == m.attached {
			line += dimStyle.Render(" (attached)")
		}
		if i == m.cursor {
			line = selStyle.Render("› " + s.Name)
			if s.Name == m.attached {
				line += dimStyle.Render(" (attached)")
			}
		}
		b.WriteString(line + "\n")
	}

	left := lipgloss.NewStyle().Width(m.leftWidth()).Render(b.String())
	view := left
	if len(m.sessions) > 0 && m.width > 50 {
		preview := m.renderPreview(m.sessions[m.cursor].Name)
		view = lipgloss.JoinHorizontal(lipgloss.Top, left, previewStyle.Render(preview))
	}
	return view + "\n" + dimStyle.Render(header) + m.footer()
}

func (m Model) branchView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" New worktree — pick or type a branch ") + "\n\n")
	b.WriteString(m.input.View() + "\n\n")
	for i, br := range m.filtered {
		if i == m.bcursor {
			b.WriteString(selStyle.Render("› "+br) + "\n")
		} else {
			b.WriteString("  " + br + "\n")
		}
		if i >= 12 {
			b.WriteString(dimStyle.Render("  …") + "\n")
			break
		}
	}
	b.WriteString("\n" + dimStyle.Render(" enter: use selected (or typed name)   esc: back"))
	return b.String() + m.footer()
}

func (m Model) footer() string {
	if m.status == "" {
		return ""
	}
	return "\n\n" + errStyle.Render(" "+m.status)
}

func (m Model) leftWidth() int {
	if m.width <= 50 {
		return m.width
	}
	return m.width * 45 / 100
}

// renderPreview captures the session's active pane and clips it to the preview
// box. The capture is full-terminal-width/height, so each line is truncated to
// the box width (ANSI-aware, to keep colors intact) and the whole thing to the
// available height — otherwise long lines wrap into the list column.
func (m Model) renderPreview(session string) string {
	raw := tmux.CapturePane(session)
	if raw == "" {
		return ""
	}
	// previewStyle adds a 1-col left border + 1-col left padding.
	w := m.width - m.leftWidth() - 2
	if w < 1 {
		w = 1
	}
	lines := strings.Split(raw, "\n")
	if h := m.previewHeight(); len(lines) > h {
		lines = lines[:h]
	}
	for i, ln := range lines {
		lines[i] = ansi.Truncate(ln, w, "")
	}
	return strings.Join(lines, "\n")
}

// previewHeight is how many capture lines fit beside the list, leaving room for
// the title, the header/footer, and the update banner when shown.
func (m Model) previewHeight() int {
	h := m.height - 4
	if m.updateAvail {
		h--
	}
	if h < 1 {
		return 1
	}
	return h
}

// ── helpers ─────────────────────────────────────────────────────────

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
