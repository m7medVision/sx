// Package agent classifies AI-agent state from tmux pane text.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// State is the detected status of a session.
type State int

const (
	Idle State = iota
	Waiting
	Working
	Plan
	Blocked
	Completed
)

// Source identifies how an assessment was obtained.
type Source string

const (
	Heuristic   Source = "heuristic"
	EventSource Source = "event"
)

// Confidence qualifies an assessment for presentation.
type Confidence string

const (
	High Confidence = "high"
	Low  Confidence = "low"
)

// Coverage reports whether all relevant panes were inspected.
type Coverage string

const (
	Complete Coverage = "complete"
	Partial  Coverage = "partial"
)

// Assessment is the source-aware agent state attached to a session snapshot.
type Assessment struct {
	State      State      `json:"state"`
	Source     Source     `json:"source"`
	Confidence Confidence `json:"confidence"`
	Coverage   Coverage   `json:"coverage"`
	Summary    string     `json:"summary,omitempty"`
	Evidence   string     `json:"evidence,omitempty"`
}

// Markers are the lowercase substrings that map pane text to a state.
type Markers struct {
	NeedsInput []string `yaml:"needs_input"`
	Completed  []string `yaml:"completed"`
	Working    []string `yaml:"working"`
	Plan       []string `yaml:"plan"`
	Present    []string `yaml:"present"`
}

// Defaults are the built-in reliable markers. Config extends them.
func Defaults() Markers {
	return Markers{
		NeedsInput: []string{"do you want to proceed", "❯ 1.", "│ 1.", "1. yes"},
		Completed:  []string{"task completed", "all tasks completed", "completed successfully"},
		Working:    []string{"interrupt)", "esc to interrupt)"},
		Plan:       []string{"plan mode"},
		Present: []string{
			"? for shortcuts", "shift+tab to cycle", "mode on (shift+tab",
			"claude code", "opencode", "esc to undo",
		},
	}
}

// ParseState turns the command-facing lifecycle state into an internal state.
func ParseState(value string) (State, error) {
	for state, name := range map[State]string{
		Idle: "idle", Waiting: "waiting", Working: "working", Plan: "plan", Blocked: "blocked", Completed: "completed",
	} {
		if value == name {
			return state, nil
		}
	}
	return Idle, fmt.Errorf("invalid lifecycle state %q (want idle, waiting, working, plan, blocked, or completed)", value)
}

// String returns the stable command and JSON representation of a state.
func (s State) String() string {
	for state, name := range map[State]string{
		Idle: "idle", Waiting: "waiting", Working: "working", Plan: "plan", Blocked: "blocked", Completed: "completed",
	} {
		if s == state {
			return name
		}
	}
	return "unknown"
}

// MarshalJSON emits the stable lifecycle state name rather than its internal number.
func (s State) MarshalJSON() ([]byte, error) {
	if s.String() == "unknown" {
		return nil, fmt.Errorf("cannot marshal invalid lifecycle state %d", s)
	}
	return json.Marshal(s.String())
}

// UnmarshalJSON accepts the stable lifecycle state name stored on disk.
func (s *State) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	state, err := ParseState(value)
	if err != nil {
		return err
	}
	*s = state
	return nil
}

// Detect preserves the original single-pane classifier API.
func Detect(paneText string, m Markers) State {
	return Assess([]string{paneText}, m, true).State
}

// Assess combines relevant-pane evidence into a source-aware heuristic.
func Assess(panes []string, m Markers, complete bool) Assessment {
	assessment := Assessment{State: Idle, Source: Heuristic, Confidence: High, Coverage: Complete}
	if !complete {
		assessment.Confidence, assessment.Coverage = Low, Partial
	}
	for _, pane := range panes {
		state, evidence := classify(strings.ToLower(pane), m)
		if priority(state) > priority(assessment.State) {
			assessment.State, assessment.Evidence = state, evidence
		}
	}
	return assessment
}

// WithEvent replaces a heuristic assessment with a reported lifecycle event.
func WithEvent(fallback Assessment, event Event) Assessment {
	return Assessment{State: event.State, Source: EventSource, Confidence: High, Coverage: Complete, Summary: event.Summary}
}

// priority makes cross-pane conflict resolution explicit: an actionable block
// wins over live work, while completion never conceals a prompt elsewhere.
func priority(state State) int {
	switch state {
	case Blocked:
		return 5
	case Working:
		return 4
	case Plan:
		return 3
	case Waiting:
		return 2
	case Completed:
		return 1
	default:
		return 0
	}
}

func classify(text string, m Markers) (State, string) {
	for _, rule := range []struct {
		state State
		terms []string
	}{
		{Blocked, m.NeedsInput}, {Completed, m.Completed}, {Working, m.Working},
		{Plan, m.Plan}, {Waiting, m.Present},
	} {
		if evidence := matching(text, rule.terms); evidence != "" {
			return rule.state, evidence
		}
	}
	return Idle, ""
}

func containsAny(text string, subs []string) bool { return matching(text, subs) != "" }

func matching(text string, subs []string) string {
	for _, s := range subs {
		if s != "" && strings.Contains(text, strings.ToLower(s)) {
			return s
		}
	}
	return ""
}
