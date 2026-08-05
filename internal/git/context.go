package git

import "strings"

// Context is the Git and worktree metadata sx presents for a session path.
type Context struct {
	Branch         string
	Dirty          bool
	LinkedWorktree bool
	WorktreePath   string
}

// ContextForDir returns Git metadata for dir. It returns an error when dir is
// not in a repository, allowing callers to retain the session without Git
// context.
func ContextForDir(dir string) (Context, error) {
	if !IsRepo(dir) {
		return Context{}, errNotRepository(dir)
	}
	branch, err := git(dir, "branch", "--show-current")
	if err != nil {
		return Context{}, err
	}
	status, err := git(dir, "status", "--porcelain")
	if err != nil {
		return Context{}, err
	}
	root, linked := IsLinkedWorktree(dir)
	return Context{
		Branch:         branch,
		Dirty:          strings.TrimSpace(status) != "",
		LinkedWorktree: linked,
		WorktreePath:   root,
	}, nil
}

type errNotRepository string

func (e errNotRepository) Error() string {
	return string(e) + " is not a Git repository"
}
