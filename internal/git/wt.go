// Package git wraps the `git` CLI for the worktree operations sx needs.
package git

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
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

// Branches lists local + remote branches, stripping the origin/ prefix and
// dropping the bogus `origin` and `HEAD` entries. Sorted and de-duplicated.
func Branches(repoRoot string) []string {
	out, err := git(repoRoot, "branch", "--all", "--format=%(refname:short)")
	if err != nil || out == "" {
		return nil
	}
	seen := map[string]bool{}
	var branches []string
	for _, b := range strings.Split(out, "\n") {
		b = strings.TrimPrefix(b, "origin/")
		if b == "" || b == "origin" || b == "HEAD" || seen[b] {
			continue
		}
		seen[b] = true
		branches = append(branches, b)
	}
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

// EnsureWorktree resolves (creating if needed) a worktree at wtPath for branch,
// using the same fallback chain as the original bash switcher:
//
//	reuse existing worktree → local branch → origin/<branch> → brand-new branch.
func EnsureWorktree(repoRoot, branch, wtPath string) (string, error) {
	if _, err := os.Stat(wtPath); err == nil {
		return wtPath, nil // already there
	}
	if existing := WorktreeForBranch(repoRoot, branch); existing != "" {
		return existing, nil
	}

	var err error
	switch {
	case hasRef(repoRoot, "refs/heads/"+branch):
		_, err = git(repoRoot, "worktree", "add", wtPath, branch)
	case hasRef(repoRoot, "refs/remotes/origin/"+branch):
		_, err = git(repoRoot, "worktree", "add", "-b", branch, wtPath, "origin/"+branch)
	default:
		_, err = git(repoRoot, "worktree", "add", "-b", branch, wtPath)
	}
	if err != nil {
		return "", err
	}
	return wtPath, nil
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
