package tmux

import (
	"context"
	"strconv"

	"example.com/tmux/internal/codec"
)

func (s *Server) endpointAction(ctx context.Context, name string, args ...string) error {
	op, e := s.begin(ctx)
	if e != nil {
		return opError(name, e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return opError(name, e)
	}
	_, e = s.execute(op, emptyPlan(command(name, args...)), &guard{identity: info.Identity}, nil)
	return opError(name, e)
}

// Kill explicitly kills the selected server. It is not a cleanup method for a
// control connection; Connection.Close never invokes it.
func (s *Server) Kill(ctx context.Context) error { return s.endpointAction(ctx, "kill-server") }
func (s Session) Kill(ctx context.Context) error { return s.h.act(ctx, "kill-session", "-t", s.h.id) }
func (s Session) Rename(ctx context.Context, name string) error {
	if e := sessionName(name, false); e != nil {
		return opError("RenameSession", e)
	}
	return s.h.act(ctx, "rename-session", "-t", s.h.id, "--", codec.LiteralFormat(name))
}
func (w Window) Rename(ctx context.Context, name string) error {
	v, e := literal(name)
	if e != nil {
		return opError("RenameWindow", e)
	}
	return w.h.act(ctx, "rename-window", "-t", w.h.id, "--", v)
}

// Kill destroys the shared window in ALL sessions. Use WindowLink.Unlink to
// remove only one membership.
func (w Window) Kill(ctx context.Context) error { return w.h.act(ctx, "kill-window", "-t", w.h.id) }
func (p Pane) Kill(ctx context.Context) error   { return p.h.act(ctx, "kill-pane", "-t", p.h.id) }
func (p Pane) Select(ctx context.Context) error { return p.h.act(ctx, "select-pane", "-t", p.h.id) }
func (p Pane) SetTitle(ctx context.Context, title string) error {
	v, e := literal(title)
	if e != nil {
		return opError("SetTitle", e)
	}
	return p.h.act(ctx, "select-pane", "-t", p.h.id, "-T", v)
}
func (p Pane) SetInputEnabled(ctx context.Context, enabled bool) error {
	flag := "-d"
	if enabled {
		flag = "-e"
	}
	return p.h.act(ctx, "select-pane", "-t", p.h.id, flag)
}
func (l WindowLink) Select(ctx context.Context) error {
	return l.act(ctx, "select-window", "-t", l.target())
}

// Unlink removes only this observed membership. tmux may reject removal of its
// last link; use UnlinkWith(Force:true) to explicitly permit that destruction.
func (l WindowLink) Unlink(ctx context.Context) error { return l.UnlinkWith(ctx, UnlinkOptions{}) }

type UnlinkOptions struct{ Force bool }

func (l WindowLink) UnlinkWith(ctx context.Context, o UnlinkOptions) error {
	args := []string{"-t", l.target()}
	if o.Force {
		args = append(args, "-k")
	}
	return l.act(ctx, "unlink-window", args...)
}
func (s Session) LastWindow(ctx context.Context) error {
	return s.h.act(ctx, "last-window", "-t", s.h.id)
}
func (w Window) LastPane(ctx context.Context) error { return w.h.act(ctx, "last-pane", "-t", w.h.id) }
func (w Window) SelectLayout(ctx context.Context, layout Layout) error {
	if layout == "" || !codec.ValidString(string(layout)) {
		return opError("SelectLayout", invalid("layout"))
	}
	return w.h.act(ctx, "select-layout", "-t", w.h.id, "--", string(layout))
}
func (w Window) NextLayout(ctx context.Context) error {
	return w.h.act(ctx, "select-layout", "-t", w.h.id, "-n")
}
func (w Window) PreviousLayout(ctx context.Context) error {
	return w.h.act(ctx, "select-layout", "-t", w.h.id, "-p")
}
func (w Window) Resize(ctx context.Context, size Size) error {
	if !size.valid() || size.Width == 0 && size.Height == 0 {
		return opError("ResizeWindow", invalid("size"))
	}
	args := []string{"-t", w.h.id}
	if size.Width > 0 {
		args = append(args, "-x", strconv.Itoa(size.Width))
	}
	if size.Height > 0 {
		args = append(args, "-y", strconv.Itoa(size.Height))
	}
	return w.h.act(ctx, "resize-window", args...)
}
func (p Pane) Resize(ctx context.Context, size Size) error {
	if !size.valid() || size.Width == 0 && size.Height == 0 {
		return opError("ResizePane", invalid("size"))
	}
	args := []string{"-t", p.h.id}
	if size.Width > 0 {
		args = append(args, "-x", strconv.Itoa(size.Width))
	}
	if size.Height > 0 {
		args = append(args, "-y", strconv.Itoa(size.Height))
	}
	return p.h.act(ctx, "resize-pane", args...)
}
func (p Pane) ToggleZoom(ctx context.Context) error {
	return p.h.act(ctx, "resize-pane", "-t", p.h.id, "-Z")
}

type RespawnOptions struct {
	Program     Program
	Dir         string
	Env         map[string]string
	KillRunning bool
}

func respawn(h handle, ctx context.Context, name string, o RespawnOptions) error {
	extra, argv, e := programArgs(o.Dir, o.Env, o.Program)
	if e != nil {
		return opError(name, e)
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

// Respawn with zero Program requests tmux's stored respawn program, which differs
// from new-pane creation's zero Program (the default shell).
func (p Pane) Respawn(ctx context.Context, o RespawnOptions) error {
	return respawn(p.h, ctx, "respawn-pane", o)
}
func (w Window) Respawn(ctx context.Context, o RespawnOptions) error {
	return respawn(w.h, ctx, "respawn-window", o)
}

type LinkOptions struct {
	Index   *int
	Select  bool
	Replace bool
}

func linkTarget(s Session, index *int) (string, error) {
	if e := s.h.check(); e != nil {
		return "", e
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
func (w Window) Link(ctx context.Context, s Session, o LinkOptions) (WindowLink, error) {
	if e := sameHandles(w.h, s.h); e != nil {
		return WindowLink{}, opError("LinkWindow", e)
	}
	target, e := linkTarget(s, o.Index)
	if e != nil {
		return WindowLink{}, opError("LinkWindow", e)
	}
	// link-window has no -P. An explicit index permits a guarded, same-queue
	// inspection; automatic allocation has no unambiguous creation-output API.
	if o.Index == nil {
		return WindowLink{}, opError("LinkWindow", &UnsupportedError{Feature: "link-window without an explicit index"})
	}
	args := []string{"-s", w.h.id, "-t", target}
	if !o.Select {
		args = append(args, "-d")
	}
	if o.Replace {
		args = append(args, "-k")
	}
	return mutateLink(ctx, w.h, w.h.guard(), "link-window", args, target)
}
func mutateLink(ctx context.Context, h handle, g *guard, name string, args []string, target string) (WindowLink, error) {
	op, e := h.server.begin(ctx)
	if e != nil {
		return WindowLink{}, opError(name, e)
	}
	defer op.close()
	nodes := []wireNode{leaf(command(name, args...)), leaf(command("display-message", "-p", "-t", target, codec.RecordFormat(fieldsFor(WindowKind))))}
	r, e := h.server.execute(op, plan{nodes: nodes, mode: replyRecords}, g, nil)
	if e != nil {
		return WindowLink{}, opError(name, e)
	}
	rows, e := parseRaw(r.Stdout, fieldsFor(WindowKind), "link")
	if e != nil || len(rows) != 1 {
		if e == nil {
			e = ErrProtocol
		}
		return WindowLink{}, afterError(name, e)
	}
	_, link, e := h.server.decodeWindow(rows[0], &h.origin)
	if e != nil {
		return WindowLink{}, afterError(name, e)
	}
	return link.Handle(), nil
}
func (l WindowLink) Move(ctx context.Context, s Session, o LinkOptions) (WindowLink, error) {
	if e := l.check(); e != nil {
		return WindowLink{}, opError("MoveWindow", e)
	}
	if e := sameHandles(l.h, s.h); e != nil {
		return WindowLink{}, opError("MoveWindow", e)
	}
	target, e := linkTarget(s, o.Index)
	if e != nil {
		return WindowLink{}, opError("MoveWindow", e)
	}
	if o.Index == nil {
		return WindowLink{}, opError("MoveWindow", &UnsupportedError{Feature: "move-window without explicit index"})
	}
	args := []string{"-s", l.target(), "-t", target}
	if !o.Select {
		args = append(args, "-d")
	}
	if o.Replace {
		args = append(args, "-k")
	}
	return mutateLink(ctx, l.h, l.guard(), "move-window", args, target)
}
func (l WindowLink) Swap(ctx context.Context, other WindowLink, selectWindow bool) error {
	if e := l.check(); e != nil {
		return opError("SwapWindow", e)
	}
	if e := other.check(); e != nil {
		return opError("SwapWindow", e)
	}
	if e := sameHandles(l.h, other.h); e != nil {
		return opError("SwapWindow", e)
	}
	op, e := l.h.server.begin(ctx)
	if e != nil {
		return opError("SwapWindow", e)
	}
	defer op.close()
	g := l.guard()
	g.links = append(g.links, other.guard().links...)
	args := []string{"-s", l.target(), "-t", other.target()}
	if !selectWindow {
		args = append(args, "-d")
	}
	_, e = l.h.server.execute(op, emptyPlan(command("swap-window", args...)), g, nil)
	return opError("SwapWindow", e)
}

type JoinOptions struct {
	Direction Direction
	Size      SplitSize
	Before    bool
	Select    bool
}

func (p Pane) Join(ctx context.Context, target Pane, o JoinOptions) error {
	if e := sameHandles(p.h, target.h); e != nil {
		return opError("JoinPane", e)
	}
	if o.Direction > Horizontal {
		return opError("JoinPane", invalid("direction"))
	}
	size, e := o.Size.args()
	if e != nil {
		return opError("JoinPane", e)
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
	return p.h.act(ctx, "join-pane", args...)
}
func (p Pane) Move(ctx context.Context, target Pane, o JoinOptions) error {
	return p.Join(ctx, target, o)
}
func (p Pane) Swap(ctx context.Context, other Pane, selectPane bool) error {
	if e := sameHandles(p.h, other.h); e != nil {
		return opError("SwapPane", e)
	}
	args := []string{"-s", p.h.id, "-t", other.h.id}
	if !selectPane {
		args = append(args, "-d")
	}
	return p.h.act(ctx, "swap-pane", args...)
}

type BreakOptions struct {
	Index  *int
	Name   string
	Select bool
}

func (p Pane) Break(ctx context.Context, s Session, o BreakOptions) (WindowLink, error) {
	if e := sameHandles(p.h, s.h); e != nil {
		return WindowLink{}, opError("BreakPane", e)
	}
	target, e := linkTarget(s, o.Index)
	if e != nil {
		return WindowLink{}, opError("BreakPane", e)
	}
	name, e := literal(o.Name)
	if e != nil {
		return WindowLink{}, opError("BreakPane", e)
	}
	op, e := p.h.server.begin(ctx)
	if e != nil {
		return WindowLink{}, opError("BreakPane", e)
	}
	defer op.close()
	args := []string{"-s", p.h.id, "-t", target, "-P", "-F", codec.RecordFormat(fieldsFor(WindowKind))}
	if !o.Select {
		args = append(args, "-d")
	}
	if o.Name != "" {
		args = append(args, "-n", name)
	}
	r, e := p.h.server.execute(op, recordsPlan(command("break-pane", args...)), p.h.guard(), nil)
	if e != nil {
		return WindowLink{}, opError("BreakPane", e)
	}
	rows, e := parseRaw(r.Stdout, fieldsFor(WindowKind), "window")
	if e != nil || len(rows) != 1 {
		if e == nil {
			e = ErrProtocol
		}
		return WindowLink{}, afterError("BreakPane", e, recoverCreated(r.Stdout, WindowKind)...)
	}
	_, link, e := p.h.server.decodeWindow(rows[0], &p.h.origin)
	if e != nil {
		return WindowLink{}, afterError("BreakPane", e, recoverCreated(r.Stdout, WindowKind)...)
	}
	return link.Handle(), nil
}

type PipeOptions struct {
	OnlyIfNotPiped bool
	Input          bool
	Output         bool
}

// Pipe deliberately starts a server-side shell script. Cancellation of this
// local command does not stop that script; StopPipe is an explicit operation.
func (p Pane) Pipe(ctx context.Context, script string, o PipeOptions) error {
	if !codec.ValidString(script) || script == "" {
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
func (p Pane) StopPipe(ctx context.Context) error { return p.h.act(ctx, "pipe-pane", "-t", p.h.id) }
