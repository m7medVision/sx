// Package inventory builds the stable presentation snapshot for sx sessions.
package inventory

import (
	"sort"
	"time"

	"github.com/m7medVision/sx/internal/agent"
	"github.com/m7medVision/sx/internal/git"
	"github.com/m7medVision/sx/internal/tmux"
)

// Snapshot is the complete session inventory presented by sx.
type Snapshot struct {
	Sessions []Session `json:"sessions"`
}

// Session is one session's presentation data. Git context is optional so a
// tmux session remains actionable even when its path cannot be inspected.
type Session struct {
	Name           string           `json:"name"`
	Path           string           `json:"path"`
	Activity       time.Time        `json:"activity"`
	Assessment     agent.Assessment `json:"assessment"`
	HasGitContext  bool             `json:"hasGitContext"`
	Branch         string           `json:"branch,omitempty"`
	Dirty          bool             `json:"dirty"`
	LinkedWorktree bool             `json:"linkedWorktree"`
	WorktreePath   string           `json:"worktreePath,omitempty"`
	Attention      *Attention       `json:"attention,omitempty"`
}

// Attention is the durable evidence behind an unread lifecycle report.
type Attention struct {
	Summary      string    `json:"summary,omitempty"`
	At           time.Time `json:"at"`
	Acknowledged bool      `json:"acknowledged"`
}

// NeedsAttention reports whether a session is blocked or has an unread report.
func (s Session) NeedsAttention() bool {
	return s.Assessment.State == agent.Blocked || (s.Attention != nil && !s.Attention.Acknowledged)
}

// Current returns the snapshot shared by the TUI and machine-readable CLI.
// Explicit lifecycle events override pane-text assessment for their session.
func Current(markers agent.Markers) Snapshot {
	events, err := agent.DefaultStore().Load()
	if err != nil {
		events = map[string]agent.Event{}
	}
	return OverlayEvents(Build(tmux.ListSessions(), git.ContextForDir, func(session tmux.Session) agent.Assessment {
		panes, complete := tmux.CaptureSessionPlain(session.Name)
		return agent.Assess(panes, markers, complete)
	}), events)
}

// Build returns a stable snapshot in tmux's session order.
func Build(sessions []tmux.Session, inspect func(string) (git.Context, error), assess func(tmux.Session) agent.Assessment) Snapshot {
	rows := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		row := Session{Name: session.Name, Path: session.Path, Activity: session.Activity, Assessment: assess(session)}
		if context, err := inspect(session.Path); err == nil {
			row.HasGitContext = true
			row.Branch = context.Branch
			row.Dirty = context.Dirty
			row.LinkedWorktree = context.LinkedWorktree
			row.WorktreePath = context.WorktreePath
		}
		rows = append(rows, row)
	}
	return Snapshot{Sessions: rows}
}

// OverlayEvents applies explicit event precedence to a session snapshot.
func OverlayEvents(snapshot Snapshot, events map[string]agent.Event) Snapshot {
	for i := range snapshot.Sessions {
		if event, ok := events[snapshot.Sessions[i].Name]; ok {
			snapshot.Sessions[i].Assessment = agent.WithEvent(snapshot.Sessions[i].Assessment, event)
			snapshot.Sessions[i].Attention = &Attention{Summary: event.Summary, At: event.At, Acknowledged: event.Acknowledged}
		}
	}
	return snapshot
}

// AttentionFirst returns a stable attention-prioritized view: blocked sessions,
// then unread reports, then all ordinary sessions in their original order.
func AttentionFirst(snapshot Snapshot) Snapshot {
	out := Snapshot{Sessions: append([]Session(nil), snapshot.Sessions...)}
	sort.SliceStable(out.Sessions, func(i, j int) bool {
		return attentionRank(out.Sessions[i]) < attentionRank(out.Sessions[j])
	})
	return out
}

// AttentionOnly returns just the sessions requiring developer attention.
func AttentionOnly(snapshot Snapshot) Snapshot {
	out := Snapshot{}
	for _, session := range snapshot.Sessions {
		if session.NeedsAttention() {
			out.Sessions = append(out.Sessions, session)
		}
	}
	return AttentionFirst(out)
}

func attentionRank(session Session) int {
	if session.Assessment.State == agent.Blocked {
		return 0
	}
	if session.Attention != nil && !session.Attention.Acknowledged {
		return 1
	}
	return 2
}

// AttentionTransitions identifies sessions that newly need attention.
func AttentionTransitions(before, after Snapshot) []Session {
	seen := make(map[string]bool, len(before.Sessions))
	for _, session := range before.Sessions {
		seen[session.Name] = session.NeedsAttention()
	}
	var transitions []Session
	for _, session := range after.Sessions {
		if session.NeedsAttention() && !seen[session.Name] {
			transitions = append(transitions, session)
		}
	}
	return transitions
}
