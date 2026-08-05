package git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/m7medVision/sx/internal/git"
)

func TestBranchesPreservesRemoteIdentity(t *testing.T) {
	repo := testRepo(t)
	runGit(t, repo, "update-ref", "refs/remotes/origin/feature/x", "HEAD")
	runGit(t, repo, "update-ref", "refs/remotes/upstream/feature/x", "HEAD")

	got := git.Branches(repo)
	for _, want := range []string{"main", "origin/feature/x", "upstream/feature/x"} {
		if !slices.Contains(got, want) {
			t.Fatalf("Branches() = %q, missing %q", got, want)
		}
	}
}

func TestEnsureWorktreeUsesExistingRemoteBranch(t *testing.T) {
	repo := testRepo(t)
	runGit(t, repo, "update-ref", "refs/remotes/origin/feature/x", "HEAD")
	path := filepath.Join(repo, ".worktrees", "origin-feature-x")

	resolved, err := git.EnsureWorktree(repo, "origin/feature/x", path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf("resolved = %q, want %q", resolved, path)
	}
	if _, err := os.Stat(filepath.Join(resolved, "README")); err != nil {
		t.Fatalf("remote worktree checkout missing: %v", err)
	}

	reused, err := git.EnsureWorktree(repo, "origin/feature/x", filepath.Join(repo, ".worktrees", "other-path"))
	if err != nil {
		t.Fatal(err)
	}
	if reused != path {
		t.Fatalf("reused = %q, want %q", reused, path)
	}
}

func TestBranchesDisambiguatesRemoteFromLikeNamedLocalBranch(t *testing.T) {
	repo := testRepo(t)
	runGit(t, repo, "branch", "origin/feature/x")
	runGit(t, repo, "update-ref", "refs/remotes/origin/feature/x", "HEAD")

	got := git.Branches(repo)
	for _, want := range []string{"local/origin/feature/x", "origin/feature/x"} {
		if !slices.Contains(got, want) {
			t.Fatalf("Branches() = %q, missing %q", got, want)
		}
	}

	path := filepath.Join(repo, ".worktrees", "remote")
	if _, err := git.EnsureWorktree(repo, "origin/feature/x", path); err != nil {
		t.Fatal(err)
	}
	out := string(runGit(t, path, "branch", "--show-current"))
	if out != "sx/origin/feature/x\n" {
		t.Fatalf("remote source checked out %q", out)
	}
}

func TestEnsureWorktreeRejectsPathForDifferentSource(t *testing.T) {
	repo := testRepo(t)
	runGit(t, repo, "branch", "feature/a")
	runGit(t, repo, "branch", "feature/b")
	path := filepath.Join(repo, ".worktrees", "feature")
	if _, err := git.EnsureWorktree(repo, "feature/a", path); err != nil {
		t.Fatal(err)
	}

	if _, err := git.EnsureWorktree(repo, "feature/b", path); err == nil {
		t.Fatal("EnsureWorktree reused a path belonging to another source")
	}
}

func TestEnsureWorktreeRejectsUnknownSource(t *testing.T) {
	repo := testRepo(t)
	path := filepath.Join(repo, ".worktrees", "missing")

	if _, err := git.EnsureWorktree(repo, "missing", path); err == nil {
		t.Fatal("EnsureWorktree accepted a worktree source branch that does not exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unknown source created %q", path)
	}
}

func testRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README")
	runGit(t, repo, "commit", "-m", "init")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}
