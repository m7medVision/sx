# sx

A small lazygit-style TUI for **tmux sessions + git-worktree agent workflows**,
inspired by [workmux](https://github.com/raine/workmux). Each task gets its own
git worktree (isolation) running in its own tmux session (the interface); you
start your AI agent (claude/opencode/…) inside that session yourself.

Replaces the old `.config/tmux/session-switcher.sh`.

## Install

```sh
go install github.com/m7medVision/sx@latest
```

This drops the `sx` binary in `$(go env GOPATH)/bin` (usually `~/go/bin`); make
sure that's on your `PATH`. Prebuilt binaries for Linux/macOS are also attached
to each [GitHub release](https://github.com/m7medVision/sx/releases).

From a checkout instead:

```sh
make install        # → $GOPATH/bin/sx
```

Bound in `~/.tmux.conf`:

```tmux
bind-key s run-shell '$HOME/go/bin/sx'
```

Press **`prefix s`** (your prefix is `C-s`) to open it.

## Keys

In the session list:

| Key      | Action                                                    |
|----------|-----------------------------------------------------------|
| `enter`  | switch to the selected session                            |
| `ctrl-n` | new plain session (prompts for a name, in the current dir)|
| `ctrl-w` | new git-worktree session (pick or type a branch)          |
| `ctrl-x` | kill the selected session (asks before removing a worktree)|
| `esc`/`q`| cancel                                                    |

`ctrl-w` only appears inside a git repo. New worktrees go to
`<repo>/.worktrees/<branch>` and `.worktrees/` is auto-added to `.gitignore`.
You can pick an existing branch or type a brand-new one.

## Update checks

On launch `sx` checks GitHub (at most once a day, cached in
`~/.cache/sx/update.json`) for a newer release and shows a one-line banner if
one exists — upgrade with `go install github.com/m7medVision/sx@latest`. The
check runs in the background, so it never delays the popup and silently does
nothing when offline. Disable it entirely with `SX_NO_UPDATE_CHECK=1`.

## Config

Worktrees are clean checkouts with **no gitignored files** (`.env`,
`node_modules`, …). Tell `sx` what to bring in via config — global
`~/.config/sx/config.yaml` and/or per-project `.sx.yaml` (project wins):

```yaml
worktree_dir: .worktrees      # relative to repo root (default)
files:
  copy:
    - .env
    - .env.*
  symlink:
    - node_modules            # symlink big dirs instead of copying
```

Globs resolve relative to the repo root; matched paths are recreated at the
same relative path inside the new worktree.

## How the switch works

`tmux switch-client` is unreliable inside `display-popup -E`, so `sx` runs in
two steps from one binary: the launcher (`sx`) opens the TUI popup
(`sx menu`); the TUI records the chosen session to `$SX_TARGET_FILE` and quits;
the launcher then performs `switch-client` from the reliable post-popup context.
