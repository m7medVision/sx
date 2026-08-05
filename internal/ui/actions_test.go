package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/m7medVision/sx/internal/config"
)

func TestFilterActionsSearchesCatalog(t *testing.T) {
	got := filterActions("work", nil)
	if len(got) != 2 || got[0] != "new-worktree" || got[1] != "review-worktree" {
		t.Fatalf("work search = %#v", got)
	}
}

func TestActionCatalogIncludesNamedProjectActions(t *testing.T) {
	actions := actionCatalog(map[string]config.ProjectAction{"start-review": {Source: "origin/review"}})
	if !slices.Contains(actions, "start-review") {
		t.Fatalf("catalog = %#v", actions)
	}
	if got := filterActions("review", map[string]config.ProjectAction{"start-review": {Source: "origin/review"}}); !slices.Contains(got, "start-review") {
		t.Fatalf("review search = %#v", got)
	}
}

func TestUnavailableProjectActionLeavesNoWorktreeOrTarget(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README")
	runGit("commit", "-m", "init")

	app := &App{mode: modeList, repoRoot: repo, actions: map[string]config.ProjectAction{"missing": {Source: "origin/missing"}}}
	if err := app.DispatchAction("missing", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(app.status, "worktree source branch") || app.Target != "" {
		t.Fatalf("status/target = %q/%q", app.status, app.Target)
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees")); !os.IsNotExist(err) {
		t.Fatalf("unavailable action created worktree directory: %v", err)
	}
}

func TestDispatchActionInvokesSupportedAction(t *testing.T) {
	app := &App{mode: modeList, previewOn: true}
	if err := app.DispatchAction("toggle-preview", nil, nil); err != nil {
		t.Fatal(err)
	}
	if app.previewOn {
		t.Fatal("toggle-preview action did not dispatch")
	}
	if err := app.DispatchAction("missing", nil, nil); err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("unknown action error = %v", err)
	}
}
