package git

import (
	"fmt"
	"strings"
)

// Review contains read-only Git information for a selected linked worktree.
type Review struct {
	Status string
	Diff   string
}

// ReviewForWorktree returns porcelain status and the diff from HEAD. It runs no
// mutating Git commands and accepts only a linked worktree root.
func ReviewForWorktree(worktree string) (Review, error) {
	if _, linked := IsLinkedWorktree(worktree); !linked {
		return Review{}, fmt.Errorf("%q is not a linked worktree", worktree)
	}
	status, err := git(worktree, "status", "--short")
	if err != nil {
		return Review{}, err
	}
	diff, err := git(worktree, "diff", "--no-ext-diff", "HEAD")
	if err != nil {
		return Review{}, err
	}
	return Review{Status: status, Diff: diff}, nil
}

// IsClean reports whether review status contains no changes.
func (r Review) IsClean() bool { return strings.TrimSpace(r.Status) == "" }
