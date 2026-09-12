package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	PaneAbove PaneDirection = iota
	PaneBelow
	PaneLeft
	PaneRight
)

// PaneDirection identifies a neighboring pane relative to a target pane.
type PaneDirection uint8

type (
	// UnlinkOptions configures unlinking a window from a session slot.
	UnlinkOptions struct {
		// Force allows unlinking even if this is the last link to the window (-k flag).
		// By default, tmux rejects unlinking a window's only link to prevent accidental
		// destruction of running processes. Setting Force permits that destruction.
		Force bool
	}

	// RespawnOptions configures re-executing a command in an existing window or pane.
	RespawnOptions struct {
		// Program specifies the new command. Zero value re-runs the stored respawn command.
		Program Program

		// Dir specifies the working directory for the process.
		Dir string

		// Env specifies environment variable overrides.
		Env map[string]string

		// KillRunning kills the process if still running (-k flag).
		KillRunning bool
	}

	// LinkOptions configures linking an existing window into another session.
	LinkOptions struct {
		// Index selects an exact slot; nil chooses the first free slot at or above base-index.
		Index *int

		// Select controls whether the linked window becomes active in the target session.
		Select bool

		// Replace permits replacing any window already occupying the target slot (-k flag).
		Replace bool
	}

	// JoinOptions configures joining a source pane into another window beside a target pane.
	JoinOptions struct {
		// Direction specifies vertical (top/bottom) or horizontal (side-by-side) split.
		Direction Direction

		// Size specifies the size for the joined pane.
		Size SplitSize

		// Before places the pane before (above or left of) the target pane (-b flag).
		Before bool

		// Select controls whether the joined pane gains focus immediately.
		Select bool
	}

	// BreakOptions configures breaking a pane out of its window into a new standalone window.
	BreakOptions struct {
		// Index optionally specifies the slot index for the new window.
		Index *int

		// Name optionally specifies the name for the new window.
		Name string

		// Select controls whether the new window gains focus immediately.
		Select bool
	}

	// PipeOptions configures piping pane terminal output to a shell command.
	PipeOptions struct {
		// OnlyIfNotPiped avoids starting if a pipe is already open (-o flag).
		OnlyIfNotPiped bool

		// Input pipes input sent to the pane into the command (-I flag).
		Input bool

		// Output pipes output produced by the pane into the command (-O flag).
		Output bool
	}
)

func (s *Server) endpointAction(ctx context.Context, name string, args ...string) error {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError(name, err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return opError(name, err)
	}

	_, err = s.execute(opCtx, op, emptyPlan(command(name, args...)), newGuard(info.Identity), nil)

	return opError(name, err)
}

// Kill explicitly terminates the entire tmux server daemon and all sessions/windows/panes managed by it.
// This is an explicit administrative action; normal cleanup of a [Connection] or [Server] never invokes it.
func (s *Server) Kill(ctx context.Context) error { return s.endpointAction(ctx, "kill-server") }

// Kill terminates this session and all windows that have no remaining links in other sessions.
func (s Session) Kill(ctx context.Context) error { return s.h.act(ctx, "kill-session", "-t", s.h.id) }

// Rename changes the human-readable name of this session.
// Session names cannot contain colons or periods.
func (s Session) Rename(ctx context.Context, name string) error {
	if err := sessionName(name, false); err != nil {
		return opError("RenameSession", err)
	}

	return s.h.act(ctx, "rename-session", "-t", s.h.id, "--", wire.LiteralFormat(name))
}

// Rename changes the title/name of this window.
func (w Window) Rename(ctx context.Context, name string) error {
	v, err := literal(name)
	if err != nil {
		return opError("RenameWindow", err)
	}

	return w.h.act(ctx, "rename-window", "-t", w.h.id, "--", v)
}

// Kill destroys the shared window object across ALL sessions where it is linked.
// To remove the window from only one session without killing it globally,
// use [WindowLink.Unlink].
func (w Window) Kill(ctx context.Context) error { return w.h.act(ctx, "kill-window", "-t", w.h.id) }

// Kill terminates this pane and sends SIGHUP to its child process.
func (p Pane) Kill(ctx context.Context) error { return p.h.act(ctx, "kill-pane", "-t", p.h.id) }

// Select gives user focus to this pane within its window.
func (p Pane) Select(ctx context.Context) error { return p.h.act(ctx, "select-pane", "-t", p.h.id) }

// SetTitle updates the pane's title string (#{pane_title}).
func (p Pane) SetTitle(ctx context.Context, title string) error {
	v, err := literal(title)
	if err != nil {
		return opError("SetTitle", err)
	}

	return p.h.act(ctx, "select-pane", "-t", p.h.id, "-T", v)
}

// SetInputEnabled enables or disables keyboard and mouse input transmission to this pane.
// Disabling input prevents keystrokes from reaching the child process while allowing output to stream.
func (p Pane) SetInputEnabled(ctx context.Context, enabled bool) error {
	flag := "-d"
	if enabled {
		flag = "-e"
	}

	return p.h.act(ctx, "select-pane", "-t", p.h.id, flag)
}

// Select switches the active/focused window in this session to this link slot.
func (l WindowLink) Select(ctx context.Context) error {
	return l.act(ctx, "select-window", "-t", l.target())
}

// Unlink removes only this observed membership from the session.
// By default, tmux rejects unlinking a window if this is its last remaining link;
// use [WindowLink.UnlinkWith] with Force: true to explicitly permit that destruction.
func (l WindowLink) Unlink(ctx context.Context) error {
	return l.UnlinkWith(ctx, UnlinkOptions{Force: false})
}

// UnlinkWith removes this window link from its session slot using explicit options.
func (l WindowLink) UnlinkWith(ctx context.Context, o UnlinkOptions) error {
	args := []string{"-t", l.target()}
	if o.Force {
		args = append(args, "-k")
	}

	return l.act(ctx, "unlink-window", args...)
}

// LastWindow selects the previously active window in this session.
func (s Session) LastWindow(ctx context.Context) error {
	return s.h.act(ctx, "last-window", "-t", s.h.id)
}

// LastPane selects the previously active pane in this window.
func (w Window) LastPane(ctx context.Context) error { return w.h.act(ctx, "last-pane", "-t", w.h.id) }

// SelectLayout applies a named layout arrangement to the panes in this window.
func (w Window) SelectLayout(ctx context.Context, layout Layout) error {
	if layout == "" || !wire.ValidString(string(layout)) {
		return opError("SelectLayout", invalid("layout"))
	}

	return w.h.act(ctx, "select-layout", "-t", w.h.id, "--", string(layout))
}

// NextLayout cycles this window to the next preset layout arrangement.
func (w Window) NextLayout(ctx context.Context) error {
	return w.h.act(ctx, "select-layout", "-t", w.h.id, "-n")
}

// PreviousLayout cycles this window to the previous preset layout arrangement.
func (w Window) PreviousLayout(ctx context.Context) error {
	return w.h.act(ctx, "select-layout", "-t", w.h.id, "-p")
}

// Resize sets the window's total dimensions in character cells.
func (w Window) Resize(ctx context.Context, size Size) error {
	args, err := resizeArgs(w.h.id, size)
	if err != nil {
		return opError("ResizeWindow", err)
	}

	return w.h.act(ctx, "resize-window", args...)
}

// Resize sets the pane's dimensions in character cells.
func (p Pane) Resize(ctx context.Context, size Size) error {
	args, err := resizeArgs(p.h.id, size)
	if err != nil {
		return opError("ResizePane", err)
	}

	return p.h.act(ctx, "resize-pane", args...)
}

func resizeArgs(id string, size Size) ([]string, error) {
	if !size.valid() || size.Width == 0 && size.Height == 0 {
		return nil, invalid("size")
	}

	args := []string{"-t", id}
	if size.Width > 0 {
		args = append(args, "-x", strconv.Itoa(size.Width))
	}

	if size.Height > 0 {
		args = append(args, "-y", strconv.Itoa(size.Height))
	}

	return args, nil
}

// ToggleZoom toggles whether this pane is zoomed to fill the entire window (-Z flag).
func (p Pane) ToggleZoom(ctx context.Context) error {
	return p.h.act(ctx, "resize-pane", "-t", p.h.id, "-Z")
}

func respawn(h handle, ctx context.Context, name string, o RespawnOptions) error {
	extra, argv, err := programArgs(o.Dir, o.Env, o.Program)
	if err != nil {
		return opError(name, err)
	}

	args := []string{"-t", h.id}
	if o.KillRunning {
		args = append(args, "-k")
	}

	args = append(args, extra...)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return h.act(ctx, name, args...)
}

// Respawn re-executes the command in this pane, replacing the existing or dead process.
// Unlike pane creation where a zero Program executes the default shell, Respawn with
// a zero Program requests tmux's previously stored respawn command.
func (p Pane) Respawn(ctx context.Context, o RespawnOptions) error {
	return respawn(p.h, ctx, "respawn-pane", o)
}

// Respawn re-executes the command in the window's initial pane.
func (w Window) Respawn(ctx context.Context, o RespawnOptions) error {
	return respawn(w.h, ctx, "respawn-window", o)
}

func linkTarget(s Session, index *int) (string, error) {
	if err := s.h.check(); err != nil {
		return "", err
	}

	target := s.h.id + ":"

	if index != nil {
		if *index < 0 || *index > 1<<30 {
			return "", invalid("index")
		}

		target += strconv.Itoa(*index)
	}

	return target, nil
}

// Link creates a new link to this window in session s at the specified slot index.
// A nil Index chooses a free slot. Returns a handle for the confirmed destination slot.
func (w Window) Link(ctx context.Context, s Session, o LinkOptions) (WindowLink, error) {
	target, err := linkTarget(s, o.Index)
	if err != nil {
		return WindowLink{}, opError("LinkWindow", err)
	}

	if o.Index == nil && o.Replace {
		return WindowLink{}, opError("LinkWindow", invalid("Replace requires an explicit index"))
	}

	args := []string{"-s", w.h.id, "-t", target}
	if !o.Select {
		args = append(args, "-d")
	}

	if o.Replace {
		args = append(args, "-k")
	}

	opCtx, op, err := beginHandles(ctx, &w.h, &s.h)
	if err != nil {
		return WindowLink{}, opError("LinkWindow", err)
	}
	defer op.close()

	return mutateLink(opCtx, op, w.h, w.h.guard(), "link-window", args, target)
}

func mutateLink(ctx context.Context, op *operation, h handle, g *guard, name string, args []string, target string) (WindowLink, error) {
	if strings.HasSuffix(target, ":") {
		index, err := freeWindowIndex(ctx, op, h.server, h.origin, target)
		if err != nil {
			return WindowLink{}, opError(name, err)
		}

		target += strconv.Itoa(index)

		for i := range args {
			if args[i] == "-t" {
				args[i+1] = target
				break
			}
		}
	}

	nodes := []wireNode{leaf(command(name, args...)), leaf(command("display-message", "-p", "-t", target, wire.RecordFormat(fieldsFor(WindowKind))))}

	r, err := h.server.execute(ctx, op, plan{nodes: nodes, mode: replyRecords, allowStart: false}, g, nil)
	if err != nil {
		return WindowLink{}, opError(name, err)
	}

	return parseLinkedWindow(h, name, target, r.Stdout)
}

func parseLinkedWindow(h handle, name, target string, data []byte) (WindowLink, error) {
	rows, err := parseRaw(data, fieldsFor(WindowKind), "link")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = ErrProtocol
		}

		return WindowLink{}, afterError(name, err)
	}

	_, link, err := h.server.decodeWindow(rows[0], &h.origin)
	if err != nil {
		return WindowLink{}, afterError(name, err)
	}

	if string(link.WindowID) != h.id || string(link.SessionID)+":"+strconv.Itoa(link.Index) != target {
		return WindowLink{}, afterError(name, ErrLinkChanged)
	}

	return link.Handle(), nil
}

// Move moves this window link to another session s at the specified slot index.
// A nil Index chooses a free slot. Returns a handle for the confirmed destination slot.
func (l WindowLink) Move(ctx context.Context, s Session, o LinkOptions) (WindowLink, error) {
	if err := l.check(); err != nil {
		return WindowLink{}, opError("MoveWindow", err)
	}

	target, err := linkTarget(s, o.Index)
	if err != nil {
		return WindowLink{}, opError("MoveWindow", err)
	}

	if o.Index == nil && o.Replace {
		return WindowLink{}, opError("MoveWindow", invalid("Replace requires an explicit index"))
	}

	args := []string{"-s", l.target(), "-t", target}
	if !o.Select {
		args = append(args, "-d")
	}

	if o.Replace {
		args = append(args, "-k")
	}

	opCtx, op, err := beginHandles(ctx, &l.h, &s.h)
	if err != nil {
		return WindowLink{}, opError("MoveWindow", err)
	}
	defer op.close()

	return mutateLink(opCtx, op, l.h, l.guard(), "move-window", args, target)
}

// Swap exchanges the slot positions of this window link and another window link.
func (l WindowLink) Swap(ctx context.Context, other WindowLink, selectWindow bool) error {
	if err := l.check(); err != nil {
		return opError("SwapWindow", err)
	}

	if err := other.check(); err != nil {
		return opError("SwapWindow", err)
	}

	opCtx, op, err := beginHandles(ctx, &l.h, &other.h)
	if err != nil {
		return opError("SwapWindow", err)
	}
	defer op.close()

	g := l.guard()
	g.links = append(g.links, other.guard().links...)

	args := []string{"-s", l.target(), "-t", other.target()}
	if !selectWindow {
		args = append(args, "-d")
	}

	_, err = l.h.server.execute(opCtx, op, emptyPlan(command("swap-window", args...)), g, nil)

	return opError("SwapWindow", err)
}

// Join moves this pane from its current window into target's window as a split.
func (p Pane) Join(ctx context.Context, target Pane, o JoinOptions) error {
	if o.Direction > Horizontal {
		return opError("JoinPane", invalid("direction"))
	}

	size, err := o.Size.args()
	if err != nil {
		return opError("JoinPane", err)
	}

	args := []string{"-s", p.h.id, "-t", target.h.id}
	if !o.Select {
		args = append(args, "-d")
	}

	if o.Before {
		args = append(args, "-b")
	}

	if o.Direction == Horizontal {
		args = append(args, "-h")
	} else {
		args = append(args, "-v")
	}

	args = append(args, size...)

	opCtx, op, err := beginHandles(ctx, &p.h, &target.h)
	if err != nil {
		return opError("JoinPane", err)
	}
	defer op.close()

	_, err = p.h.server.execute(opCtx, op, emptyPlan(command("join-pane", args...)), p.h.guard(), nil)

	return opError("join-pane", err)
}

// Move is an alias for [Pane.Join], moving this pane beside target pane.
func (p Pane) Move(ctx context.Context, target Pane, o JoinOptions) error {
	return p.Join(ctx, target, o)
}

// Swap exchanges the positions and dimensions of this pane and another pane.
func (p Pane) Swap(ctx context.Context, other Pane, selectPane bool) error {
	opCtx, op, err := beginHandles(ctx, &p.h, &other.h)
	if err != nil {
		return opError("SwapPane", err)
	}
	defer op.close()

	args := []string{"-s", p.h.id, "-t", other.h.id}
	if !selectPane {
		args = append(args, "-d")
	}

	_, err = p.h.server.execute(opCtx, op, emptyPlan(command("swap-pane", args...)), p.h.guard(), nil)

	return opError("swap-pane", err)
}

// Break removes this pane from its current window and creates a new window containing only
// this pane in session s. Returns a [WindowLink] handle for the new window.
func (p Pane) Break(ctx context.Context, s Session, o BreakOptions) (WindowLink, error) {
	target, err := linkTarget(s, o.Index)
	if err != nil {
		return WindowLink{}, opError("BreakPane", err)
	}

	name, err := literal(o.Name)
	if err != nil {
		return WindowLink{}, opError("BreakPane", err)
	}

	opCtx, op, err := beginHandles(ctx, &p.h, &s.h)
	if err != nil {
		return WindowLink{}, opError("BreakPane", err)
	}
	defer op.close()

	args := breakArgs(p.h.id, target, name, o)

	r, err := p.h.server.execute(opCtx, op, recordsPlan(command("break-pane", args...)), p.h.guard(), nil)
	if err != nil {
		return WindowLink{}, opError("BreakPane", err)
	}

	rows, err := parseRaw(r.Stdout, fieldsFor(WindowKind), "window")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = ErrProtocol
		}

		return WindowLink{}, afterError("BreakPane", err, recoverCreated(r.Stdout, WindowKind)...)
	}

	_, link, err := p.h.server.decodeWindow(rows[0], &p.h.origin)
	if err != nil {
		return WindowLink{}, afterError("BreakPane", err, recoverCreated(r.Stdout, WindowKind)...)
	}

	if err = opCtx.Err(); err != nil {
		return link.Handle(), afterError("BreakPane", err, createdFromHandle(link.link.h))
	}

	return link.Handle(), nil
}

func breakArgs(paneID, target, name string, o BreakOptions) []string {
	args := []string{"-s", paneID, "-t", target, "-P", "-F", wire.RecordFormat(fieldsFor(WindowKind))}
	if !o.Select {
		args = append(args, "-d")
	}

	if o.Name != "" {
		args = append(args, "-n", name)
	}

	return args
}

// Pipe deliberately starts a background shell command on the server and pipes pane I/O into it.
// To stop piping, callers must explicitly invoke [Pane.StopPipe].
func (p Pane) Pipe(ctx context.Context, script string, o PipeOptions) error {
	if !wire.ValidString(script) || script == "" {
		return opError("Pipe", invalid("pipe script"))
	}

	args := []string{"-t", p.h.id}
	if o.OnlyIfNotPiped {
		args = append(args, "-o")
	}

	if o.Input {
		args = append(args, "-I")
	}

	if o.Output {
		args = append(args, "-O")
	}

	args = append(args, "--", script)

	return p.h.act(ctx, "pipe-pane", args...)
}

// StopPipe stops any active background shell command piping I/O from this pane.
func (p Pane) StopPipe(ctx context.Context) error { return p.h.act(ctx, "pipe-pane", "-t", p.h.id) }

// NextWindow selects the next window in this session.
func (s Session) NextWindow(ctx context.Context) error {
	return s.h.act(ctx, "next-window", "-t", s.h.id)
}

// PreviousWindow selects the previous window in this session.
func (s Session) PreviousWindow(ctx context.Context) error {
	return s.h.act(ctx, "previous-window", "-t", s.h.id)
}

// SelectAdjacent selects a neighboring pane using tmux's directional navigation.
func (p Pane) SelectAdjacent(ctx context.Context, direction PaneDirection) error {
	var flag string

	switch direction {
	case PaneAbove:
		flag = "-U"
	case PaneBelow:
		flag = "-D"
	case PaneLeft:
		flag = "-L"
	case PaneRight:
		flag = "-R"
	default:
		return opError("SelectAdjacent", invalid("pane direction"))
	}

	return p.h.act(ctx, "select-pane", "-t", p.h.id, flag)
}

// The native link/move command claims this slot without -k. If another client
// claims it first, tmux rejects the mutation rather than overwriting that client.
func freeWindowIndex(ctx context.Context, op *operation, server *Server, id ServerIdentity, target string) (int, error) {
	r, err := server.execute(ctx, op, recordsPlan(command("display-message", "-p", "-t", target, wire.RecordFormat([]string{"base-index"}))), newGuard(id), nil)
	if err != nil {
		return 0, err
	}

	rows, err := wire.ParseRecords(r.Stdout, 1)
	if err != nil || len(rows) != 1 {
		return 0, ErrProtocol
	}

	index, err := strconv.Atoi(rows[0][0])
	if err != nil || index < 0 {
		return 0, ErrProtocol
	}

	_, links, err := server.windows(ctx, op, id, target)
	if err != nil {
		return 0, err
	}

	used := make(map[int]bool, len(links))
	for _, link := range links {
		used[link.Index] = true
	}

	for used[index] {
		index++
	}

	if index > 1<<30 {
		return 0, invalid("index")
	}

	return index, nil
}
