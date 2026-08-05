package inventory_test

import (
	"errors"
	"testing"
	"time"

	"github.com/m7medVision/sx/internal/agent"
	"github.com/m7medVision/sx/internal/git"
	"github.com/m7medVision/sx/internal/inventory"
	"github.com/m7medVision/sx/internal/tmux"
)

func TestBuildPreservesSessionsWhenGitMetadataIsUnavailable(t *testing.T) {
	activity := time.Unix(1_700_000_000, 0)
	sessions := []tmux.Session{
		{Name: "clean", Path: "/repo", Activity: activity},
		{Name: "shell", Path: "/tmp", Activity: activity},
	}

	snapshot := inventory.Build(sessions, func(path string) (git.Context, error) {
		if path == "/tmp" {
			return git.Context{}, errors.New("not a repository")
		}
		return git.Context{Branch: "feature/inventory", Dirty: false, LinkedWorktree: true, WorktreePath: "/repo"}, nil
	}, func(session tmux.Session) agent.Assessment {
		return agent.Assessment{State: agent.Waiting, Source: agent.Heuristic, Confidence: agent.High, Coverage: agent.Complete}
	})

	if len(snapshot.Sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(snapshot.Sessions))
	}
	clean := snapshot.Sessions[0]
	if clean.Name != "clean" || clean.Branch != "feature/inventory" || clean.Dirty || !clean.LinkedWorktree || clean.WorktreePath != "/repo" || clean.Assessment.State != agent.Waiting || !clean.Activity.Equal(activity) {
		t.Fatalf("clean inventory row = %#v", clean)
	}
	shell := snapshot.Sessions[1]
	if shell.Name != "shell" || shell.Path != "/tmp" || shell.HasGitContext {
		t.Fatalf("missing Git context hid or changed session: %#v", shell)
	}
}

func TestOverlayEventsTakesPrecedenceOverHeuristic(t *testing.T) {
	snapshot := inventory.Snapshot{Sessions: []inventory.Session{{
		Name:       "api",
		Assessment: agent.Assessment{State: agent.Working, Source: agent.Heuristic, Confidence: agent.High, Coverage: agent.Complete},
	}}}
	got := inventory.OverlayEvents(snapshot, map[string]agent.Event{
		"api": {Session: "api", State: agent.Completed, Summary: "released"},
	})
	assessment := got.Sessions[0].Assessment
	if assessment.State != agent.Completed || assessment.Source != agent.EventSource || assessment.Summary != "released" {
		t.Fatalf("assessment = %#v", assessment)
	}
}
