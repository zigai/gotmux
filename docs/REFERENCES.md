# Version-pinned implementation references

These sources were used to inspect behavior. They do not replace executable
compatibility evidence. No upstream source files are bundled in this archive.

- Supplied contract: ../SPEC.md, dated 2026-09-08.
- Releases: https://github.com/tmux/tmux/releases
- Pinned 3.6 manual: https://raw.githubusercontent.com/tmux/tmux/3.6/tmux.1
- Byte length and format expansion: https://raw.githubusercontent.com/tmux/tmux/3.6/format.c
- Process argv and command text: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-parse.y
- Synchronous guard insertion: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-if-shell.c
- Queue groups and control framing: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-queue.c
- Control input and output: https://raw.githubusercontent.com/tmux/tmux/3.6/control.c
- Client output/UTF-8 behavior: https://raw.githubusercontent.com/tmux/tmux/3.6/server-client.c
- Single-operand programs and spawn environments: https://raw.githubusercontent.com/tmux/tmux/3.6/spawn.c
- Session creation: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-new-session.c
- Window creation: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-new-window.c
- Session name normalization: https://raw.githubusercontent.com/tmux/tmux/3.6/session.c
- Options: https://raw.githubusercontent.com/tmux/tmux/3.6/options-table.c
- Option reads: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-show-options.c
- Option writes/hooks: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-set-option.c
- Environment reads: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-show-environment.c
- Binary buffer reads: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-save-buffer.c
- Binary buffer input: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-load-buffer.c
- Empty buffer behavior: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-set-buffer.c
- Buffer store: https://raw.githubusercontent.com/tmux/tmux/3.6/paste.c
- Client attachment: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-attach-session.c
- Flow control and format subscriptions: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-refresh-client.c
- Binding serialization and command inventory: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-list-keys.c
- Explicit daemon startup: https://raw.githubusercontent.com/tmux/tmux/3.6/cmd-kill-server.c
- Go downloads: https://go.dev/dl/?mode=json
- x/sync v0.17.0: https://github.com/golang/sync/blob/v0.17.0/go.mod
- x/term v0.35.0: https://github.com/golang/term/blob/v0.35.0/go.mod
- Rapid v1.2.0: https://github.com/flyingmutant/rapid/blob/v1.2.0/go.mod
- go-cmp v0.7.0: https://github.com/google/go-cmp/blob/v0.7.0/go.mod
