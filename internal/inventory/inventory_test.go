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

func TestAttentionViewsPrioritizeBlockedThenUnreadAndRetainEvidence(t *testing.T) {
	at := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	snapshot := inventory.Snapshot{Sessions: []inventory.Session{
		{Name: "ordinary", Assessment: agent.Assessment{State: agent.Working}},
		{Name: "unread", Assessment: agent.Assessment{State: agent.Completed}, Attention: &inventory.Attention{Summary: "done", At: at}},
		{Name: "blocked", Assessment: agent.Assessment{State: agent.Blocked}, Attention: &inventory.Attention{Summary: "approve", At: at, Acknowledged: true}},
	}}
	ordered := inventory.AttentionFirst(snapshot)
	for i, want := range []string{"blocked", "unread", "ordinary"} {
		if ordered.Sessions[i].Name != want {
			t.Fatalf("ordered[%d] = %q, want %q", i, ordered.Sessions[i].Name, want)
		}
	}
	only := inventory.AttentionOnly(snapshot)
	if len(only.Sessions) != 2 || only.Sessions[1].Attention.Summary != "done" || !only.Sessions[1].Attention.At.Equal(at) {
		t.Fatalf("attention view = %#v", only.Sessions)
	}
}

func TestAttentionTransitionsAndFallbackBlockedState(t *testing.T) {
	before := inventory.Snapshot{Sessions: []inventory.Session{{Name: "api", Assessment: agent.Assessment{State: agent.Working}}}}
	after := inventory.OverlayEvents(inventory.Snapshot{Sessions: []inventory.Session{{Name: "api", Assessment: agent.Assessment{State: agent.Blocked, Source: agent.Heuristic}}}}, map[string]agent.Event{
		"api": {Session: "api", State: agent.Working, Summary: "still running", At: time.Now()},
	})
	if after.Sessions[0].Assessment.State != agent.Blocked {
		t.Fatalf("fallback block was lost: %#v", after.Sessions[0].Assessment)
	}
	if transitions := inventory.AttentionTransitions(before, after); len(transitions) != 1 || transitions[0].Name != "api" {
		t.Fatalf("transitions = %#v", transitions)
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
