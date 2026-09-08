# Command-to-Go index

This is an alpha inventory. Flag lists describe implemented builders, not
exhaustive upstream parity. Real-tmux tests listed below have not run here.

| Command | Go surface | Path | Completion | Evidence / status |
| --- | --- | --- | --- | --- |
| `attach-session` | Server.OpenControl; PrepareAttach | control; advanced subprocess | control lifetime / caller-owned terminal process | TestIntegrationControlParityAndExplicitAuxiliary; implemented-unverified-on-real-tmux |
| `bind-key` | Server.Bind | both | install binding | TestHookAndBindingPayloadsNeverEvaluate; implemented-unverified-on-real-tmux |
| `break-pane` | Pane.Break | both | creation slot | integration pending; implemented-unverified-on-real-tmux |
| `capture-pane` | Pane.Capture | subprocess; explicit auxiliary | owned screen/history bytes | TestIntegrationControlParityAndExplicitAuxiliary; implemented-unverified-on-real-tmux |
| `choose-buffer` | Pane.ChooseBuffer | subprocess UI | mode installed, not selection | integration pending; implemented-unverified-on-real-tmux |
| `choose-client` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `choose-tree` | Pane.ChooseTree | subprocess UI | mode installed, not selection | integration pending; implemented-unverified-on-real-tmux |
| `clear-history` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `clock-mode` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `command-prompt` | Client.Prompt | subprocess UI | prompt scheduling/completion, not parsed choice | integration pending; implemented-unverified-on-real-tmux |
| `confirm-before` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `copy-mode` | Pane.CopyMode | both | mode entry | integration pending; implemented-unverified-on-real-tmux |
| `customize-mode` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `delete-buffer` | Server.DeleteBuffer | both | acknowledged mutation | TestIntegrationBinaryBufferAndLiteralInput; implemented-unverified-on-real-tmux |
| `detach-client` | Client.Detach | both | client departure | integration pending; implemented-unverified-on-real-tmux |
| `display-menu` | Client.Menu | subprocess UI | tmux completion, no invented user choice | integration pending; implemented-unverified-on-real-tmux |
| `display-message` | Server.Probe; Info methods; Pane.Format; Client.Message | both for records; subprocess for UI | metadata, format, message | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `display-panes` | Client.DisplayPanes | subprocess UI | UI scheduling | integration pending; implemented-unverified-on-real-tmux |
| `display-popup` | Client.Popup | subprocess UI | tmux completion, no invented user choice | integration pending; implemented-unverified-on-real-tmux |
| `find-window` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `has-session` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `if-shell` | Server.IfFormat; internal guards | both for internal guarded typed plans | synchronous format condition / structured sequence | TestGuardQueueAndUnwrap; implemented-unverified-on-real-tmux |
| `join-pane` | Pane.Join; Pane.Move | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `kill-pane` | Pane.Kill | both | acknowledged mutation | TestIntegrationTwoServersAndReplacement; implemented-unverified-on-real-tmux |
| `kill-server` | Server.Kill; KillIfIdentity | subprocess; control may lose acknowledgment | daemon exit | TestIntegrationTwoServersAndReplacement; implemented-unverified-on-real-tmux |
| `kill-session` | Session.Kill | both | acknowledged mutation / possible connection exit | integration pending; implemented-unverified-on-real-tmux |
| `kill-window` | Window.Kill | both | shared-window destruction | integration pending; implemented-unverified-on-real-tmux |
| `last-pane` | Window.LastPane | both | focus selection | integration pending; implemented-unverified-on-real-tmux |
| `last-window` | Session.LastWindow | both | focus selection | integration pending; implemented-unverified-on-real-tmux |
| `link-window` | Window.Link | both | mutation then observed slot | TestIntegrationLinkedGraphAndStaleIndex; implemented-unverified-on-real-tmux |
| `list-buffers` | Server.Buffers | both | metadata | integration pending; implemented-unverified-on-real-tmux |
| `list-clients` | Server.Clients; Client.Info; Snapshot | both | metadata | TestIntegrationControlParityAndExplicitAuxiliary; implemented-unverified-on-real-tmux |
| `list-commands` | Server.Capabilities | both | metadata | integration pending; implemented-unverified-on-real-tmux |
| `list-keys` | Server.Bindings | subprocess; explicit auxiliary | serialized payload observation | TestHookAndBindingPayloadsNeverEvaluate; implemented-unverified-on-real-tmux |
| `list-panes` | Server.Panes; Window.Panes; Snapshot | both | metadata | TestIntegrationControlParityAndExplicitAuxiliary; implemented-unverified-on-real-tmux |
| `list-sessions` | Server.Sessions; FindSession; Snapshot | both | metadata | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `list-windows` | Server.Windows; Session.Windows; Window.Links; Snapshot | both | metadata | TestIntegrationLinkedGraphAndStaleIndex; implemented-unverified-on-real-tmux |
| `load-buffer` | Server.WriteBuffer; LoadBufferFile | subprocess stdin; file mode both | buffer input/file completion | TestIntegrationBinaryBufferAndLiteralInput; implemented-unverified-on-real-tmux |
| `lock-client` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `lock-server` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `lock-session` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `move-pane` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `move-window` | WindowLink.Move | both | mutation then observed slot | TestIntegrationLinkedGraphAndStaleIndex; implemented-unverified-on-real-tmux |
| `new-session` | Server.NewSession | both; startup subprocess | creation ID | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `new-window` | Session.NewWindow | both | creation slot | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `next-layout` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `next-window` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `paste-buffer` | Pane.PasteBuffer | both | input paste, not program completion | integration pending; implemented-unverified-on-real-tmux |
| `pipe-pane` | Pane.Pipe; StopPipe | both | pipe scheduling | integration pending; implemented-unverified-on-real-tmux |
| `previous-layout` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `previous-window` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `refresh-client` | Client.Refresh; Connection.WatchFormat; UnwatchFormat; SetPaneOutput | both; subscriptions control | refresh / subscription scheduling | integration pending; implemented-unverified-on-real-tmux |
| `rename-session` | Session.Rename | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `rename-window` | Window.Rename | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `resize-pane` | Pane.Resize; ToggleZoom | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `resize-window` | Window.Resize | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `respawn-pane` | Pane.Respawn | both | program launch, not program completion | integration pending; implemented-unverified-on-real-tmux |
| `respawn-window` | Window.Respawn | both | program launch, not program completion | integration pending; implemented-unverified-on-real-tmux |
| `rotate-window` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `run-shell` | Server.RunShell | subprocess output; restricted control | explicit script scheduling/completion | integration pending; implemented-unverified-on-real-tmux |
| `save-buffer` | Server.SaveBufferFile | both | file write acknowledgment | integration pending; implemented-unverified-on-real-tmux |
| `select-layout` | Window.SelectLayout; NextLayout; PreviousLayout | both | acknowledged mutation | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `select-pane` | Pane.Select; SetTitle; SetInputEnabled | both | acknowledged mutation | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `select-window` | WindowLink.Select | both | focus selection | TestIntegrationLinkedGraphAndStaleIndex; implemented-unverified-on-real-tmux |
| `send-keys` | Pane.SendText; SendKeys; Submit; CopyAction | both | ordered input, not shell completion | TestIntegrationBinaryBufferAndLiteralInput; implemented-unverified-on-real-tmux |
| `server-access` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `set-buffer` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `set-environment` | EnvironmentScope.Set; SetHidden; Unset; Remove | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `set-hook` | HookScope.Set; Unset | both | install/remove, never eager execution | TestHookAndBindingPayloadsNeverEvaluate; implemented-unverified-on-real-tmux |
| `set-option` | scope-specific option writes and arrays | both | acknowledged mutation | TestIntegrationOptionsInheritanceAndEmpty; implemented-unverified-on-real-tmux |
| `set-window-option` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `show-buffer` | Server.ReadBuffer | subprocess; explicit auxiliary | owned binary bytes | TestIntegrationBinaryBufferAndLiteralInput; implemented-unverified-on-real-tmux |
| `show-environment` | EnvironmentScope.Get | subprocess; explicit auxiliary | observation | integration pending; implemented-unverified-on-real-tmux |
| `show-hooks` | HookScope.List | subprocess; explicit auxiliary | serialized payload observation | TestHookAndBindingPayloadsNeverEvaluate; implemented-unverified-on-real-tmux |
| `show-messages` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `show-options` | scope-specific option reads and arrays | subprocess; explicit auxiliary | observation | TestIntegrationOptionsInheritanceAndEmpty; implemented-unverified-on-real-tmux |
| `show-prompt-history` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `show-window-options` | Server.Run | subprocess | raw result | raw path tests; command-specific integration pending; raw-only-alpha |
| `source-file` | Server.SourceFile | subprocess | configuration execution | integration pending; implemented-unverified-on-real-tmux |
| `split-window` | Pane.Split | both | creation ID | TestIntegrationInspectCreateAndCapture; implemented-unverified-on-real-tmux |
| `start-server` | Server.NewSession (AllowStart internal startup) | subprocess | startup | TestIntegrationNoServerAndExistingOnly; implemented-unverified-on-real-tmux |
| `swap-pane` | Pane.Swap | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `swap-window` | WindowLink.Swap | both | acknowledged mutation | integration pending; implemented-unverified-on-real-tmux |
| `switch-client` | Client.Switch | both | client selection | integration pending; implemented-unverified-on-real-tmux |
| `unbind-key` | Server.Unbind | both | remove binding | integration pending; implemented-unverified-on-real-tmux |
| `unlink-window` | WindowLink.Unlink; UnlinkWith | both | one membership removal | integration pending; implemented-unverified-on-real-tmux |
| `wait-for` | Server.WaitFor; Signal; Lock; Unlock | both | wait/signal/lock acknowledgment | dispatcher model; real integration pending; implemented-unverified-on-real-tmux |

Raw-only reasons and the currently mapped flags are in `internal/schema/commands.json`.
Use `scripts/inventory_tmux.py` with an isolated fixture socket to retain exact
`list-commands` usage strings for a pinned release. A full reviewed expansion
is a release gate, not silently inferred from this table.
