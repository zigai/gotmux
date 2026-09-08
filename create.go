package tmux

import (
	"context"
	"errors"
	"strconv"

	"example.com/tmux/internal/codec"
)

type NewSessionOptions struct {
	Name    string
	Dir     string
	Window  string
	Program Program
	Env     map[string]string
	Size    Size
	Start   StartPolicy
	// Group joins an explicitly named session group. tmux forbids specifying an
	// initial window name or program when joining a group.
	Group string
}
type NewWindowOptions struct {
	Name    string
	Dir     string
	Program Program
	Env     map[string]string
	Index   *int
	Select  bool
}
type SplitOptions struct {
	Direction Direction
	Size      SplitSize
	Dir       string
	Program   Program
	Env       map[string]string
	Select    bool
	Before    bool
	FullSize  bool
}

func (s *Server) NewSession(ctx context.Context, opts NewSessionOptions) (Session, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return Session{}, opError("NewSession", e)
	}
	defer op.close()
	if e = sessionName(opts.Name, true); e != nil {
		return Session{}, opError("NewSession", e)
	}
	if opts.Start > ExistingOnly || !opts.Size.valid() {
		return Session{}, opError("NewSession", invalid("startup policy or size"))
	}
	extra, argv, e := programArgs(opts.Dir, opts.Env, opts.Program)
	if e != nil {
		return Session{}, opError("NewSession", e)
	}
	args := []string{"-d", "-P", "-F", codec.RecordFormat(fieldsFor(SessionKind))}
	if opts.Name != "" {
		args = append(args, "-s", codec.LiteralFormat(opts.Name))
	}
	if opts.Window != "" {
		v, e := literal(opts.Window)
		if e != nil {
			return Session{}, opError("NewSession", e)
		}
		args = append(args, "-n", v)
	}
	if opts.Group != "" {
		if e = sessionName(opts.Group, false); e != nil {
			return Session{}, opError("NewSession", e)
		}
		if opts.Window != "" || opts.Program.kind != 0 {
			return Session{}, opError("NewSession", invalid("group conflicts with program/window"))
		}
		args = append(args, "-t", opts.Group)
	}
	if opts.Size.Width != 0 {
		args = append(args, "-x", strconv.Itoa(opts.Size.Width))
	}
	if opts.Size.Height != 0 {
		args = append(args, "-y", strconv.Itoa(opts.Size.Height))
	}
	args = append(args, extra...)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}
	var g *guard
	info, e := s.probe(op)
	if e == nil {
		g = &guard{identity: info.Identity}
	} else if errors.Is(e, ErrNoServer) && opts.Start == AllowStart && s.conn == nil && s.bound == nil {
		v, ve := s.executableVersion(op)
		if ve != nil {
			return Session{}, opError("NewSession", ve)
		}
		if ve = supportedVersion(v); ve != nil {
			return Session{}, opError("NewSession", ve)
		}
	} else {
		return Session{}, opError("NewSession", e)
	}
	p := recordsPlan(command("new-session", args...))
	p.allowStart = g == nil && opts.Start == AllowStart
	if p.allowStart {
		// start-server supplies native startup permission; the synchronous format
		// condition checks the ANSWERING daemon, including a race with another start.
		p.nodes = []wireNode{leaf(command("start-server")), conditionNode(startupVersionCondition(), p.nodes, []wireNode{markerNode(unsupportedStartupVersion)}, "")}
		p.nested = true
	}
	r, e := s.execute(op, p, g, nil)
	if string(r.Stdout) == unsupportedStartupVersion {
		return Session{}, opError("NewSession", &UnsupportedError{Feature: "answering tmux version requires recognized stable 3.6+"})
	}
	if e != nil {
		return Session{}, creationError("NewSession", e, r.Stdout, SessionKind)
	}
	fields := fieldsFor(SessionKind)
	rows, e := parseRaw(r.Stdout, fields, "session")
	if e != nil || len(rows) != 1 {
		if e == nil {
			e = decodeError("session", "record count", codec.ErrRecord)
		}
		return Session{}, afterError("NewSession", e, recoverCreated(r.Stdout, SessionKind)...)
	}
	var expected *ServerIdentity
	if g != nil {
		expected = &g.identity
	}
	v, e := s.decodeSession(rows[0], expected)
	if e != nil {
		return Session{}, afterError("NewSession", e, recoverCreated(r.Stdout, SessionKind)...)
	}
	if e = op.ctx.Err(); e != nil {
		return v.Handle(), afterError("NewSession", e, createdFromHandle(v.h))
	}
	return v.Handle(), nil
}
func createdFromHandle(h handle) CreatedObject {
	return CreatedObject{Kind: h.kind, RawID: h.id, Identity: PresentValue(h.origin)}
}
func recoverCreated(data []byte, kind ObjectKind) []CreatedObject {
	rows, e := codec.ParseRecords(data, len(fieldsFor(kind)))
	if e != nil || len(rows) != 1 || len(rows[0]) == 0 {
		return nil
	}
	// A syntactically recovered ID is useful evidence but is NOT a verified handle.
	field := ""
	switch kind {
	case SessionKind:
		field = "session_id"
	case WindowKind, LinkKind:
		field = "window_id"
	case PaneKind:
		field = "pane_id"
	default:
		return nil
	}
	fields := fieldsFor(kind)
	for i, name := range fields {
		if name == field {
			return []CreatedObject{{Kind: kind, RawID: rows[0][i], Identity: UnavailableValue[ServerIdentity]()}}
		}
	}
	return nil
}
func (s Session) NewWindow(ctx context.Context, opts NewWindowOptions) (WindowLink, error) {
	if e := s.h.check(); e != nil {
		return WindowLink{}, opError("NewWindow", e)
	}
	op, e := s.h.server.begin(ctx)
	if e != nil {
		return WindowLink{}, opError("NewWindow", e)
	}
	defer op.close()
	extra, argv, e := programArgs(opts.Dir, opts.Env, opts.Program)
	if e != nil {
		return WindowLink{}, opError("NewWindow", e)
	}
	target := s.h.id + ":"
	if opts.Index != nil {
		if *opts.Index < 0 || *opts.Index > 1<<30 {
			return WindowLink{}, opError("NewWindow", invalid("index"))
		}
		target += strconv.Itoa(*opts.Index)
	}
	args := []string{"-P", "-F", codec.RecordFormat(fieldsFor(WindowKind)), "-t", target}
	if !opts.Select {
		args = append(args, "-d")
	}
	if opts.Name != "" {
		v, e := literal(opts.Name)
		if e != nil {
			return WindowLink{}, opError("NewWindow", e)
		}
		args = append(args, "-n", v)
	}
	args = append(args, extra...)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}
	r, e := s.h.server.execute(op, recordsPlan(command("new-window", args...)), s.h.guard(), nil)
	if e != nil {
		return WindowLink{}, creationError("NewWindow", e, r.Stdout, WindowKind)
	}
	rows, e := parseRaw(r.Stdout, fieldsFor(WindowKind), "window")
	if e != nil || len(rows) != 1 {
		if e == nil {
			e = decodeError("window", "record count", codec.ErrRecord)
		}
		return WindowLink{}, afterError("NewWindow", e, recoverCreated(r.Stdout, WindowKind)...)
	}
	_, link, e := s.h.server.decodeWindow(rows[0], &s.h.origin)
	if e != nil {
		return WindowLink{}, afterError("NewWindow", e, recoverCreated(r.Stdout, WindowKind)...)
	}
	if e = op.ctx.Err(); e != nil {
		return link.Handle(), afterError("NewWindow", e, createdFromHandle(link.link.h))
	}
	return link.Handle(), nil
}
func (p Pane) Split(ctx context.Context, opts SplitOptions) (Pane, error) {
	if e := p.h.check(); e != nil {
		return Pane{}, opError("Split", e)
	}
	op, e := p.h.server.begin(ctx)
	if e != nil {
		return Pane{}, opError("Split", e)
	}
	defer op.close()
	if opts.Direction > Horizontal {
		return Pane{}, opError("Split", invalid("direction"))
	}
	size, e := opts.Size.args()
	if e != nil {
		return Pane{}, opError("Split", e)
	}
	extra, argv, e := programArgs(opts.Dir, opts.Env, opts.Program)
	if e != nil {
		return Pane{}, opError("Split", e)
	}
	args := []string{"-P", "-F", codec.RecordFormat(fieldsFor(PaneKind)), "-t", p.h.id}
	if opts.Direction == Horizontal {
		args = append(args, "-h")
	} else {
		args = append(args, "-v")
	}
	if !opts.Select {
		args = append(args, "-d")
	}
	if opts.Before {
		args = append(args, "-b")
	}
	if opts.FullSize {
		args = append(args, "-f")
	}
	args = append(args, size...)
	args = append(args, extra...)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}
	r, e := p.h.server.execute(op, recordsPlan(command("split-window", args...)), p.h.guard(), nil)
	if e != nil {
		return Pane{}, creationError("Split", e, r.Stdout, PaneKind)
	}
	rows, e := parseRaw(r.Stdout, fieldsFor(PaneKind), "pane")
	if e != nil || len(rows) != 1 {
		if e == nil {
			e = decodeError("pane", "record count", codec.ErrRecord)
		}
		return Pane{}, afterError("Split", e, recoverCreated(r.Stdout, PaneKind)...)
	}
	v, e := p.h.server.decodePane(rows[0], &p.h.origin)
	if e != nil {
		return Pane{}, afterError("Split", e, recoverCreated(r.Stdout, PaneKind)...)
	}
	if e = op.ctx.Err(); e != nil {
		return v.Handle(), afterError("Split", e, createdFromHandle(v.h))
	}
	return v.Handle(), nil
}

func creationError(name string, err error, data []byte, kind ObjectKind) error {
	outcome := outcomeOf(err)
	outcome.Created = append(outcome.Created, recoverCreated(data, kind)...)
	return &OperationError{Operation: name, Outcome: outcome, Err: err}
}
