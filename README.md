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

`ctrl-w` only appears inside a git repo. New worktrees go to
`<repo>/.worktrees/<branch>` and `.worktrees/` is auto-added to `.gitignore`.

## Config

Worktrees are clean checkouts with **no gitignored files**. Tell `sx` what to
bring in via `~/.config/sx/config.yaml` and/or per-project `.sx.yaml`
(project wins):

```yaml
worktree_dir: .worktrees
files:
  copy:
    - .env
    - .env.*
  symlink:
    - node_modules
```
