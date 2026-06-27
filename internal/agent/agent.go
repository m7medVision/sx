// Package agent classifies what an AI coding agent (Claude Code, opencode, pi,
// …) is doing inside a tmux session, by pattern-matching the session's visible
// pane text. There is no API to ask an agent its mode, so detection is purely
// heuristic over the captured screen — and tunable via config.Markers.
//
// It only sees the active pane of a session's current window; an agent running
// in a background window reads as Idle.
package agent

import "strings"

// State is the detected status of a session.
type State int

const (
	Idle    State = iota // just a shell, no agent detected
	Waiting              // agent present but paused — your turn (prompt/approval)
	Working              // agent actively running
	Plan                 // agent in planning mode
)

// Markers are the lowercase substrings that map pane text to a state. All
// matching is case-insensitive; callers pass already-merged defaults+overrides.
type Markers struct {
	NeedsInput []string `yaml:"needs_input"` // approval / explicit "your turn" prompts
	Working    []string `yaml:"working"`     // busy / interrupt hints
	Plan       []string `yaml:"plan"`        // planning-mode indicators
	Present    []string `yaml:"present"`     // "an agent is on screen" banners/hints
}

// Defaults are the built-in markers, seeded with the reliable ones. Config
// extends (never replaces) these lists.
//
// Note on Working: Claude Code's *idle* footer still prints "esc to interrupt"
// (e.g. "auto mode on … · esc to interrupt · …"), so the bare phrase is NOT a
// busy signal. Only the active spinner shows the parenthesized form ending in
// "interrupt)" — that's what we match.
func Defaults() Markers {
	return Markers{
		NeedsInput: []string{"do you want to proceed", "❯ 1.", "│ 1.", "1. yes"},
		Working:    []string{"interrupt)", "esc to interrupt)"},
		Plan:       []string{"plan mode"},
		Present: []string{
			"? for shortcuts", "shift+tab to cycle", "mode on (shift+tab",
			"claude code", "opencode", "esc to undo",
		},
	}
}

// Detect classifies a session from its plain (no-ANSI) captured pane text.
//
// Priority — first match wins:
//  1. NeedsInput  → Waiting   (a paused agent isn't showing its interrupt hint,
//     so this never collides with Working)
//  2. Working     → Working
//  3. Plan        → Plan
//  4. Present     → Waiting   (agent visible but otherwise quiet ⇒ your turn)
//  5. none        → Idle
func Detect(paneText string, m Markers) State {
	text := strings.ToLower(paneText)
	switch {
	case containsAny(text, m.NeedsInput):
		return Waiting
	case containsAny(text, m.Working):
		return Working
	case containsAny(text, m.Plan):
		return Plan
	case containsAny(text, m.Present):
		return Waiting
	default:
		return Idle
	}
}

func containsAny(text string, subs []string) bool {
	for _, s := range subs {
		if s != "" && strings.Contains(text, strings.ToLower(s)) {
			return true
		}
	}
	return false
}
