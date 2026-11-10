// Package tmux controls tmux servers from Go: it creates and inspects sessions,
// windows, and panes, sends keys, captures output, and reads and sets options.
//
// Start with [New], which describes the server to talk to. It only checks the
// configuration: no process runs and no server is contacted until the first call.
//
//	server, err := tmux.New(tmux.Config{SocketName: "dev"})
//	if err != nil {
//		return err
//	}
//
//	session, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "dev"})
//	if err != nil {
//		return err
//	}
//
//	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "shell"})
//	if err != nil {
//		return err
//	}
//
//	pane, err := link.Window().ActivePane(ctx)
//	if err != nil {
//		return err
//	}
//
//	if err := pane.SendText(ctx, "echo hello\n"); err != nil {
//		return err
//	}
//
// Use [Current] to find the session, window, and pane this process runs in, and
// [DiscoverServers] to find the servers running on the host.
//
// # Handles
//
// [Server], [Session], [WindowLink], [Window], [Pane], and [Client] are handles
// to tmux objects. A handle belongs to the running server it came from and stops
// working once that server exits. Info methods ask the server for current state;
// ID, Valid, and Equal do no I/O. An operation that takes several handles needs
// all of them to come from the same server.
//
// tmux can show one window in several sessions. [Window] is the window itself,
// and [WindowLink] is its slot in one session. Killing a Window removes it from
// every session, while unlinking a WindowLink removes it from that session only.
//
// # Commands and control mode
//
// By default each call runs one tmux command as a subprocess. [Server.OpenControl]
// opens a control mode connection instead, which keeps one client attached and
// delivers events as they happen, such as a window opening or a pane printing
// output.
//
// # Errors
//
// Every operation is bounded by the limits in [Config]. A failed operation returns
// an [OperationError], and its Outcome says whether tmux received the command, so
// the caller knows if server state may have changed.
//
// The API may change before v1.0.
package tmux
