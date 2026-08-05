// Package inventory builds the stable presentation snapshot for sx sessions.
package inventory

import (
	"time"

	"github.com/m7medVision/sx/internal/agent"
	"github.com/m7medVision/sx/internal/git"
	"github.com/m7medVision/sx/internal/tmux"
)

// Snapshot is the complete session inventory presented by sx.
type Snapshot struct {
	Sessions []Session
}

// Session is one session's presentation data. Git context is optional so a
// tmux session remains actionable even when its path cannot be inspected.
type Session struct {
	Name           string
	Path           string
	Activity       time.Time
	Assessment     agent.Assessment
	HasGitContext  bool
	Branch         string
	Dirty          bool
	LinkedWorktree bool
	WorktreePath   string
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
