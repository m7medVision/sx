package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sx/internal/config"
	"sx/internal/git"
)

// TestWorktreeAndFiles exercises the real git-worktree + file-copy pipeline
// (everything the TUI does on ctrl-w) against a throwaway repo.
func TestWorktreeAndFiles(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	os.WriteFile(filepath.Join(repo, "README"), []byte("hi"), 0o644)
	run("add", "-A")
	run("commit", "-m", "init")

	// Gitignored files that must be copied into the clean worktree.
	os.WriteFile(filepath.Join(repo, ".env"), []byte("SECRET=1"), 0o644)
	os.WriteFile(filepath.Join(repo, ".sx.yaml"),
		[]byte("files:\n  copy:\n    - .env\n"), 0o644)

	// 1. .gitignore gets the worktree dir, once.
	if err := git.EnsureWorktreesIgnored(repo, ".worktrees"); err != nil {
		t.Fatal(err)
	}
	_ = git.EnsureWorktreesIgnored(repo, ".worktrees") // idempotent
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if strings.Count(string(gi), ".worktrees/") != 1 {
		t.Fatalf(".gitignore not written once: %q", gi)
	}

	// 2. New-branch worktree is created.
	wtPath := filepath.Join(repo, ".worktrees", "feature-x")
	resolved, err := git.EnsureWorktree(repo, "feature/x", wtPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "README")); err != nil {
		t.Fatalf("worktree checkout missing: %v", err)
	}

	// 3. Config-driven copy brings .env into the clean worktree.
	cfg := config.Load(repo)
	if err := cfg.ApplyFiles(repo, resolved); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(resolved, ".env")); err != nil || string(b) != "SECRET=1" {
		t.Fatalf(".env not copied: %v %q", err, b)
	}

	// 4. The branch reported by the worktree matches.
	out, _ := exec.Command("git", "-C", resolved, "branch", "--show-current").Output()
	if strings.TrimSpace(string(out)) != "feature/x" {
		t.Fatalf("unexpected branch: %q", out)
	}

	// 5. Linked-worktree detection: the new worktree is linked, repo root is not.
	if _, linked := git.IsLinkedWorktree(resolved); !linked {
		t.Fatal("expected resolved worktree to be detected as linked")
	}
	if _, linked := git.IsLinkedWorktree(repo); linked {
		t.Fatal("main worktree should not be detected as linked")
	}
}
