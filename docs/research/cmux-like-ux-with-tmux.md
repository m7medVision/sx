# Can sx adopt a focused cmux-like UX while retaining tmux?

## Conclusion

Yes—at the **control-surface** level, not at the multiplexer level. `sx` can borrow cmux's focused navigation, concise resource context, and discoverable actions while continuing to delegate terminal lifetime, panes, layouts, scrollback, copy mode, and attachment to tmux. This is practical in the current Go/gocui popup because it is a small, read-mostly session controller, not a terminal renderer. It is also the explicit boundary of the parent spec.

## What sx already has

- A compact tmux popup switcher, with deferred `switch-client` after the popup closes: [`main.go`](../../main.go), [`internal/tmux/tmux.go`](../../internal/tmux/tmux.go). This already fills the role of cmux's focused workspace/screen chooser without nesting another mux.
- Session selection, creation, safe kill flow, git-worktree creation/reuse, and configuration-managed file copy/symlink setup: [`README.md`](../../README.md), [`internal/ui/app.go`](../../internal/ui/app.go), [`internal/git/wt.go`](../../internal/git/wt.go).
- A live, ANSI-preserving selected-session preview, including compact agent-pane rendering: [`internal/ui/app.go`](../../internal/ui/app.go#L143) (preview polling/rendering) and [`internal/tmux/tmux.go`](../../internal/tmux/tmux.go#L45) (pane capture).
- The beginning of a cmux-like concise resource row: stable inventory data includes activity plus optional branch, dirty state, linked-worktree state, and path; missing Git metadata does not remove a session. [`internal/inventory/inventory.go`](../../internal/inventory/inventory.go), [`internal/ui/app.go`](../../internal/ui/app.go#L738).
- Initial agent-state badges from active-pane text, with documented limitation that background windows are missed: [`internal/agent/agent.go`](../../internal/agent/agent.go).

This aligns especially with cmux's visible hierarchy and recent-focus model, but sx intentionally keeps tmux as the backing session model rather than owning a `session -> workspace -> screen -> pane -> tab` tree. Cmux source/docs: [`docs/concepts.md`](../../examples/cmux/cmux-tui/docs/concepts.md); official: <https://github.com/manaflow-ai/cmux/blob/main/docs/concepts.md>.

## Smallest valuable cmux-like slice

Deliver the already-planned **attention-first session inventory** as the primary list UI:

1. Show a compact per-session context line (branch, dirty/clean, linked-worktree, recent activity) and evidence/summary in the existing live preview.
2. Prioritize and filter unread `blocked`/`completed` attention; provide next-attention navigation.
3. Add a searchable action catalog that exposes the existing switch/create/kill/config actions and later review actions; retain current shortcuts and allow configured overrides.

This borrows the useful cmux ideas—visible navigable resources, stateful focus, and one discoverable action/help surface—without copying its terminal UI. Cmux documents its workspace sidebar and shortcut modal in [`docs/keyboard.md`](../../examples/cmux/cmux-tui/docs/keyboard.md) and [`docs/configuration.md`](../../examples/cmux/cmux-tui/docs/configuration.md); official: <https://github.com/manaflow-ai/cmux/blob/main/docs/keyboard.md>, <https://github.com/manaflow-ai/cmux/blob/main/docs/configuration.md>.

The slice is consistent with #1's stated “compact popup/switcher model” and inventory seam, and needs no new mux abstraction. It is covered by #3 (inventory), #4 (multi-surface heuristic state), #5 (explicit lifecycle/listing), #6 (attention inbox/navigation), and #8 (catalog/bindings). #7 is the natural follow-on for linked-worktree review. Therefore **do not create a new issue**: sequence the existing issues rather than introducing a vague “cmux-like UX” ticket. #2 and #9 cover adjacent worktree-source and repeatable-action behavior, respectively.

## Keep these tmux-owned (or out of scope)

- Pane/window/session layout, splitting, resizing, swapping, zooming, tabs, scrollback/copy mode, terminal theming, mouse routing, and persistence/restoration. Cmux implements these itself with a durable split tree, PTYs, VT-state replay, and layout undo: [`docs/concepts.md`](../../examples/cmux/cmux-tui/docs/concepts.md); official: <https://github.com/manaflow-ai/cmux/blob/main/docs/concepts.md>. #1 explicitly excludes reimplementing terminal-emulator and pane-layout features.
- Terminal attachment/replay and its PTY/VT renderer. Cmux uses Ghostty VT state and live PTY streams for attached clients: [`docs/getting-started.md`](../../examples/cmux/cmux-tui/docs/getting-started.md); official: <https://github.com/manaflow-ai/cmux/blob/main/docs/getting-started.md>. sx only invokes tmux CLI and captures active-pane text/ANSI (`internal/tmux/tmux.go`); gocui cannot practically replace that subsystem without a fundamentally different architecture.
- Browser panes, Chrome/CDP lifecycle, Kitty-graphics rendering, and browser pointer input. These are a separate cmux surface/runtime: [`docs/browser-panes.md`](../../examples/cmux/cmux-tui/docs/browser-panes.md); official: <https://github.com/manaflow-ai/cmux/blob/main/docs/browser-panes.md>. They are unrelated to sx's session/worktree-control purpose.
- Remote-machine rails, headless servers, sockets, pairing, SDK/control protocols, and cross-machine transports. Cmux has explicit headless/attach and remote-machine facilities: [`README.md`](../../examples/cmux/cmux-tui/README.md), [`docs/getting-started.md`](../../examples/cmux/cmux-tui/docs/getting-started.md); official: <https://github.com/manaflow-ai/cmux/blob/main/README.md>, <https://github.com/manaflow-ai/cmux/blob/main/docs/getting-started.md>. They exceed #1's deliberately narrow listing/notification interface.

## Scope comparison

| Capability | cmux | sx recommendation |
|---|---|---|
| Focused resource navigation | Native workspace/sidebar tree | Keep popup list; enrich its inventory and preview (#3–#6) |
| Discoverable/remappable commands | Prefix action table + shortcut modal | Action catalog and configurable bindings (#8) |
| Terminal/pane ownership | Own PTYs, layout, rendering, attach | tmux remains authoritative |
| Worktree workflow | Not its core stated model | sx-owned creation/reuse, context, safe review, named actions (#2, #7, #9) |
| Agent attention | Labels/config can surface agent programs | sx-owned lifecycle evidence, attention inbox, explicit events (#4–#6) |

## Evidence limits

The cmux findings use the ignored local first-party checkout at `examples/cmux/cmux-tui` and its upstream origin, `https://github.com/manaflow-ai/cmux.git`. Official URLs above are limited to that upstream repository. Issue scope is taken from GitHub issues #1–#9 as read on this branch's configured `m7medVision/sx` remote.
