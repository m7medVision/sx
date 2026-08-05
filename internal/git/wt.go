// Package git wraps the `git` CLI for the worktree operations sx needs.
package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// git runs a git command inside dir and returns trimmed stdout.
func git(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run()
	return err == nil
}

// RepoRoot returns the top-level directory of the repo containing dir.
func RepoRoot(dir string) (string, error) {
	return git(dir, "rev-parse", "--show-toplevel")
}

// Branches lists local + remote branches, preserving remote names so sources
// from different remotes remain unambiguous. It drops symbolic remote entries
// such as origin/HEAD. Sorted and de-duplicated.
func Branches(repoRoot string) []string {
	out, err := git(repoRoot, "branch", "--all", "--format=%(refname)")
	if err != nil || out == "" {
		return nil
	}

	remotes := map[string]bool{}
	var locals []string
	for _, ref := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			locals = append(locals, strings.TrimPrefix(ref, "refs/heads/"))
		case strings.HasPrefix(ref, "refs/remotes/"):
			remote := strings.TrimPrefix(ref, "refs/remotes/")
			if remote != "" && !strings.HasSuffix(remote, "/HEAD") {
				remotes[remote] = true
			}
		}
	}

	branches := make([]string, 0, len(locals)+len(remotes))
	for _, local := range locals {
		if remotes[local] {
			branches = append(branches, "local/"+local)
		} else {
			branches = append(branches, local)
		}
	}
	for remote := range remotes {
		branches = append(branches, remote)
	}
	sort.Strings(branches)
	return branches
}

// WorktreeForBranch returns the path of the worktree that has branch checked
// out, or "" if none does.
func WorktreeForBranch(repoRoot, branch string) string {
	out, err := git(repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	want := "refs/heads/" + branch
	var wt string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			wt = p
		} else if b, ok := strings.CutPrefix(line, "branch "); ok && b == want {
			return wt
		}
	}
	return ""
}

// hasRef reports whether the given ref exists.
func hasRef(repoRoot, ref string) bool {
	err := exec.Command("git", "-C", repoRoot, "show-ref", "--verify", "--quiet", ref).Run()
	return err == nil
}

// EnsureWorktree creates or reuses a worktree for an existing local or remote
// worktree source branch. A missing source is an error; sx never creates a
// branch solely because a developer typed its name.
func EnsureWorktree(repoRoot, source, wtPath string) (string, error) {
	branch, ref, err := resolveWorktreeSource(repoRoot, source)
	if err != nil {
		return "", err
	}
	if existing := WorktreeForBranch(repoRoot, branch); existing != "" {
		return existing, nil
	}
	if _, err := os.Stat(wtPath); err == nil {
		return "", fmt.Errorf("worktree path %q already exists for a different source", wtPath)
	}

	if ref == branch {
		_, err = git(repoRoot, "worktree", "add", wtPath, branch)
	} else {
		_, err = git(repoRoot, "worktree", "add", "-b", branch, wtPath, ref)
	}
	if err != nil {
		return "", err
	}
	return wtPath, nil
}

// resolveWorktreeSource returns the local branch to check out and the ref from
// which it should be created. Remote sources use an sx-prefixed local branch so
// selecting origin/topic and upstream/topic cannot silently mean the same
// checkout.
func resolveWorktreeSource(repoRoot, source string) (branch, ref string, err error) {
	if local, ok := strings.CutPrefix(source, "local/"); ok {
		if hasRef(repoRoot, "refs/heads/"+local) {
			return local, local, nil
		}
		return "", "", fmt.Errorf("worktree source branch %q does not exist locally", source)
	}
	if hasRef(repoRoot, "refs/remotes/"+source) {
		return "sx/" + source, "refs/remotes/" + source, nil
	}
	if hasRef(repoRoot, "refs/heads/"+source) {
		return source, source, nil
	}
	return "", "", fmt.Errorf("worktree source branch %q does not exist locally or on a configured remote", source)
}

// IsLinkedWorktree reports whether dir is a *linked* git worktree (its repo
// root's .git is a file, not a directory). Returns the worktree root too.
func IsLinkedWorktree(dir string) (root string, linked bool) {
	if !IsRepo(dir) {
		return "", false
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(filepath.Join(root, ".git"))
	if err != nil {
		return root, false
	}
	return root, !info.IsDir()
}

// RemoveWorktree removes the worktree rooted at wtPath. It fails (leaving the
// worktree on disk) when the tree has uncommitted changes.
func RemoveWorktree(repoRoot, wtPath string) error {
	_, err := git(repoRoot, "worktree", "remove", wtPath)
	return err
}

// EnsureWorktreesIgnored appends "<entry>/" to the repo's .gitignore once, so
// worktrees stored under the repo don't dirty its status.
func EnsureWorktreesIgnored(repoRoot, entry string) error {
	gi := filepath.Join(repoRoot, ".gitignore")
	line := strings.TrimSuffix(entry, "/") + "/"

	if data, err := os.ReadFile(gi); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) == line {
				return nil // already ignored
			}
		}
	}

	f, err := os.OpenFile(gi, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Add a separating newline if the file lacks a trailing one.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		buf := make([]byte, 1)
		if _, err := f.ReadAt(buf, info.Size()-1); err == nil && buf[0] != '\n' {
			if _, err := f.WriteString("\n"); err != nil {
				return err
			}
		}
	}
	_, err = f.WriteString(line + "\n")
	return err
}
