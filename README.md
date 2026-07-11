# sx

A small TUI for **tmux sessions + git-worktree agent workflows**.
Each task gets its own git worktree (isolation) running in its own tmux session.

## Install

```sh
go install github.com/m7medVision/sx@latest
```

Bound in `~/.tmux.conf`:

```tmux
bind-key s run-shell '$HOME/go/bin/sx'
```

Press **`prefix s`** to open it.

## Keys

| Key      | Action                                                     |
|----------|------------------------------------------------------------|
| `enter`  | switch to the selected session                             |
| `ctrl-n` | new plain session (prompts for a name)                     |
| `ctrl-w` | new git-worktree session (pick or type a branch)           |
| `ctrl-x` | kill the selected session (asks before removing a worktree)|
| `esc`/`q`| cancel                                                     |
| `?`      | show help                                                  |
| `c`      | edit config (global + per-project)                         |

`ctrl-w` only appears inside a git repo. New worktrees go to
`<repo>/.worktrees/<branch>` and `.worktrees/` is auto-added to `.gitignore`.

## Config

Worktrees are clean checkouts with **no gitignored files**. Tell `sx` what to
bring in via `~/.config/sx/config.yaml` and/or per-project `.sx.yaml`
(project wins).

Press **`c`** to edit config from inside the TUI. The config view has two
scopes, toggled with `tab` (only when you're inside a git repo):

- **global** — `~/.config/sx/config.yaml`
- **project** — `.sx.yaml` at the repo root

Keys in the config view:

| Key     | Action                                  |
|---------|-----------------------------------------|
| `tab`   | switch scope (global ↔ project)         |
| `↑`/`↓` | move between `worktree_dir`, `copy`, `symlink` |
| `a`     | add a file (mini-fzf picker) or edit (worktree_dir / global scope) |
| `d`     | delete the highlighted glob              |
| `e`     | edit the highlighted line (dir or globs)|
| `enter` | save the active scope to disk            |
| `q`/`esc`| close without saving                   |

When you press `a` on `files.copy` / `files.symlink` inside a git repo, a
mini-fzf opens: type to fuzzy-filter, `tab` to multi-select, `↑/↓` or `j/k`
to move, `enter` to add all marked (or the current line), `esc` to leave
the picker. The footer flashes `✓ added N file(s)` for ~2 seconds after
each commit.

Edits apply to whichever scope is active; the other scope's file is untouched.
Save writes only the configured fields, so agent-detection markers already in
the file are preserved.

```yaml
worktree_dir: .worktrees
files:
  copy:
    - .env
    - .env.*
  symlink:
    - node_modules
```
