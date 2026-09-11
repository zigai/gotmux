package tmux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

// NewSessionOptions configures the creation of a new tmux session.
type NewSessionOptions struct {
	// Name is the session name (cannot contain colons or periods). Empty defaults to automatic naming.
	Name string

	// Dir specifies the initial working directory for the session's first window.
	Dir string

	// Window specifies the name for the initial window. Disallowed when joining a Group.
	Window string

	// Program specifies the initial command. Zero value runs the default shell.
	Program Program

	// Env specifies environment variables passed to the launched program.
	// Overriding environment variables requires an explicit [Exec] or [Shell] Program;
	// using Env with the zero Program{} returns [ErrUnsupported] to prevent ambient PATH corruption.
	Env map[string]string

	// Size specifies initial window dimensions in character cells.
	Size Size

	// Start controls whether to start a new daemon if one is not running.
	Start StartPolicy

	// Group joins an existing session group. Disallows initial window name or program.
	Group string
}

// NewWindowOptions configures the creation of a new window inside an existing session.
type NewWindowOptions struct {
	// Name is the window name (#{window_name}).
	Name string

	// Dir is the working directory for the initial pane.
	Dir string

	// Program specifies the initial command. Zero value runs the default shell.
	Program Program

	// Env specifies environment overrides. Requires explicit [Exec] or [Shell].
	Env map[string]string

	// Index optionally specifies the slot index in the session.
	Index *int

	// Select controls whether the new window gains focus immediately (default false).
	Select bool
}

// SplitOptions configures splitting an existing pane into two panes.
type SplitOptions struct {
	// Direction specifies vertical (top/bottom) or horizontal (side-by-side) split.
	Direction Direction

	// Size specifies cell count or percentage. Zero splits available space evenly.
	Size SplitSize

	// Dir is the working directory for the new pane.
	Dir string

	// Program specifies the initial process. Zero value runs the default shell.
	Program Program

	// Env specifies environment overrides. Requires explicit [Exec] or [Shell].
	Env map[string]string

	// Select controls whether the new pane gains focus immediately.
	Select bool

	// Before places the new pane before (above or left of) the target pane (-b flag).
	Before bool

	// FullSize splits across the full window span (-f flag) rather than just the target pane.
	FullSize bool
}
type startGuard struct {
	guard      *guard
	allowStart bool
}

// NewSession creates a new session on the server and returns a verified [Session] handle.
//
// If opts.Start is [AllowStart] (the default) and no daemon is running, this starts a new
// background tmux daemon. If opts.Start is [ExistingOnly], it fails with [ErrNoServer] if
// the daemon is not running.
func (s *Server) NewSession(ctx context.Context, opts NewSessionOptions) (Session, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return Session{}, opError("NewSession", err)
	}
	defer op.close()

	if err = sessionName(opts.Name, true); err != nil {
		return Session{}, opError("NewSession", err)
	}

	if opts.Start > ExistingOnly || !opts.Size.valid() {
		return Session{}, opError("NewSession", invalid("startup policy or size"))
	}

	args, err := newSessionArgs(opts)
	if err != nil {
		return Session{}, opError("NewSession", err)
	}

	sg, err := s.resolveStartGuard(opCtx, op, opts.Start)
	if err != nil {
		return Session{}, opError("NewSession", err)
	}

	p := newSessionPlan(args, sg)

	r, err := s.execute(opCtx, op, p, sg.guard, nil)
	if string(r.Stdout) == unsupportedStartupVersion {
		return Session{}, opError("NewSession", unsupported("answering tmux version requires recognized stable 3.6+"))
	}

	if err != nil {
		return Session{}, creationError("NewSession", err, r.Stdout, SessionKind)
	}

	sess, err := s.parseCreatedSession(r.Stdout, sg.guard)
	if err != nil {
		return Session{}, err
	}

	if err = opCtx.Err(); err != nil {
		return sess, afterError("NewSession", err, createdFromHandle(sess.h))
	}

	return sess, nil
}

// NewWindow creates a new window in this session and returns a [WindowLink] handle for the slot.
//
// The returned handle is a [WindowLink] rather than a bare [Window] because the new window
// occupies a specific slot index within this session's window list.
func (s Session) NewWindow(ctx context.Context, opts NewWindowOptions) (WindowLink, error) {
	if err := s.h.check(); err != nil {
		return WindowLink{}, opError("NewWindow", err)
	}

	opCtx, op, err := s.h.server.begin(ctx)
	if err != nil {
		return WindowLink{}, opError("NewWindow", err)
	}
	defer op.close()

	args, err := newWindowArgs(s.h.id, opts)
	if err != nil {
		return WindowLink{}, opError("NewWindow", err)
	}

	r, err := s.h.server.execute(opCtx, op, recordsPlan(command("new-window", args...)), s.h.guard(), nil)
	if err != nil {
		return WindowLink{}, creationError("NewWindow", err, r.Stdout, WindowKind)
	}

	rows, err := parseRaw(r.Stdout, fieldsFor(WindowKind), "window")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = decodeError("window", "record count", wire.ErrRecord)
		}

		return WindowLink{}, afterError("NewWindow", err, recoverCreated(r.Stdout, WindowKind)...)
	}

	_, link, err := s.h.server.decodeWindow(rows[0], &s.h.origin)
	if err != nil {
		return WindowLink{}, afterError("NewWindow", err, recoverCreated(r.Stdout, WindowKind)...)
	}

	if err = opCtx.Err(); err != nil {
		return link.Handle(), afterError("NewWindow", err, createdFromHandle(link.link.h))
	}

	return link.Handle(), nil
}

// Split divides the target pane into two panes according to opts and returns a handle to the new pane.
func (p Pane) Split(ctx context.Context, opts SplitOptions) (Pane, error) {
	if err := p.h.check(); err != nil {
		return Pane{}, opError("Split", err)
	}

	opCtx, op, err := p.h.server.begin(ctx)
	if err != nil {
		return Pane{}, opError("Split", err)
	}
	defer op.close()

	if opts.Direction > Horizontal {
		return Pane{}, opError("Split", invalid("direction"))
	}

	args, err := splitArgs(p.h.id, opts)
	if err != nil {
		return Pane{}, opError("Split", err)
	}

	r, err := p.h.server.execute(opCtx, op, recordsPlan(command("split-window", args...)), p.h.guard(), nil)
	if err != nil {
		return Pane{}, creationError("Split", err, r.Stdout, PaneKind)
	}

	rows, err := parseRaw(r.Stdout, fieldsFor(PaneKind), "pane")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = decodeError("pane", "record count", wire.ErrRecord)
		}

		return Pane{}, afterError("Split", err, recoverCreated(r.Stdout, PaneKind)...)
	}

	v, err := p.h.server.decodePane(rows[0], &p.h.origin)
	if err != nil {
		return Pane{}, afterError("Split", err, recoverCreated(r.Stdout, PaneKind)...)
	}

	if err = opCtx.Err(); err != nil {
		return v.Handle(), afterError("Split", err, createdFromHandle(v.h))
	}

	return v.Handle(), nil
}

func creationError(name string, err error, data []byte, kind ObjectKind) error {
	outcome := outcomeOf(err)
	outcome.Created = append(outcome.Created, recoverCreated(data, kind)...)

	return &OperationError{Operation: name, Outcome: outcome, Err: err}
}

func newSessionArgs(opts NewSessionOptions) ([]string, error) {
	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	args := []string{"-d", "-P", "-F", wire.RecordFormat(fieldsFor(SessionKind))}
	if opts.Name != "" {
		args = append(args, "-s", wire.LiteralFormat(opts.Name))
	}

	if opts.Window != "" {
		v, err := literal(opts.Window)
		if err != nil {
			return nil, err
		}

		args = append(args, "-n", v)
	}

	if opts.Group != "" {
		if err = sessionName(opts.Group, false); err != nil {
			return nil, err
		}

		if opts.Window != "" || opts.Program.kind != 0 {
			return nil, invalid("group conflicts with program/window")
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

	return args, nil
}

func newSessionPlan(args []string, sg startGuard) plan {
	p := recordsPlan(command("new-session", args...))

	p.allowStart = sg.allowStart
	if p.allowStart {
		p.nodes = []wireNode{leaf(command("start-server")), conditionNode(startupVersionCondition(), p.nodes, []wireNode{markerNode(unsupportedStartupVersion)}, "")}
	}

	return p
}

func (s *Server) parseCreatedSession(stdout []byte, g *guard) (Session, error) {
	rows, err := parseRaw(stdout, fieldsFor(SessionKind), "session")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = decodeError("session", "record count", wire.ErrRecord)
		}

		return Session{}, afterError("NewSession", err, recoverCreated(stdout, SessionKind)...)
	}

	var expected *ServerIdentity
	if g != nil {
		expected = &g.identity
	}

	v, err := s.decodeSession(rows[0], expected)
	if err != nil {
		return Session{}, afterError("NewSession", err, recoverCreated(stdout, SessionKind)...)
	}

	return v.Handle(), nil
}

func (s *Server) resolveStartGuard(ctx context.Context, op *operation, start StartPolicy) (startGuard, error) {
	info, err := s.probe(ctx, op)
	switch {
	case err == nil:
		return startGuard{guard: newGuard(info.Identity), allowStart: false}, nil
	case errors.Is(err, ErrNoServer) && start == AllowStart && s.conn == nil && s.bound == nil:
		v, verErr := s.executableVersion(ctx, op)
		if verErr != nil {
			return startGuard{guard: nil, allowStart: false}, verErr
		}

		if verErr = supportedVersion(v); verErr != nil {
			return startGuard{guard: nil, allowStart: false}, verErr
		}

		return startGuard{guard: nil, allowStart: true}, nil
	default:
		return startGuard{guard: nil, allowStart: false}, err
	}
}

func newWindowArgs(sessionID string, opts NewWindowOptions) ([]string, error) {
	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	target := sessionID + ":"

	if opts.Index != nil {
		if *opts.Index < 0 || *opts.Index > 1<<30 {
			return nil, invalid("index")
		}

		target += strconv.Itoa(*opts.Index)
	}

	args := []string{"-P", "-F", wire.RecordFormat(fieldsFor(WindowKind)), "-t", target}
	if !opts.Select {
		args = append(args, "-d")
	}

	if opts.Name != "" {
		v, err := literal(opts.Name)
		if err != nil {
			return nil, err
		}

		args = append(args, "-n", v)
	}

	args = append(args, extra...)
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return args, nil
}

func splitArgs(paneID string, opts SplitOptions) ([]string, error) {
	size, err := opts.Size.args()
	if err != nil {
		return nil, err
	}

	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	args := []string{"-P", "-F", splitRecordFormat(fieldsFor(PaneKind)), "-t", paneID}
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

	return args, nil
}

func createdFromHandle(h handle) CreatedObject {
	return CreatedObject{Kind: h.kind, RawID: h.id, Identity: PresentValue(h.origin), SessionID: UnavailableValue[SessionID](), WindowIndex: UnavailableValue[int]()}
}

func recoverCreated(data []byte, kind ObjectKind) []CreatedObject {
	rows, err := wire.ParseRecords(data, len(fieldsFor(kind)))
	if err != nil || len(rows) != 1 || len(rows[0]) == 0 {
		return nil
	}

	var field string

	switch kind {
	case SessionKind:
		field = "session_id"
	case WindowKind:
		field = "window_id"
	case PaneKind:
		field = "pane_id"
	case ClientKind, LinkKind:
		return nil
	default:
		return nil
	}

	fields := fieldsFor(kind)
	for i, f := range fields {
		if f == field {
			return []CreatedObject{{Kind: kind, RawID: rows[0][i], Identity: UnavailableValue[ServerIdentity](), SessionID: UnavailableValue[SessionID](), WindowIndex: UnavailableValue[int]()}}
		}
	}

	return nil
}

func splitRecordFormat(fields []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d:", wire.RecordPrefix, len(fields))

	for _, f := range fields {
		if f == "pane_current_command" || f == "pane_current_path" {
			// pane_current_command and pane_current_path query the live process table (/proc or proc_pidinfo).
			// At the moment of split-window, the child process is concurrently forking and execing,
			// causing a race where #{n:...} and #{...} evaluate to different values (or resolve firmlinks
			// inconsistently on macOS). Emitting empty strings avoids this race while keeping field count aligned.
			b.WriteString("0:,")
			continue
		}

		fmt.Fprintf(&b, "#{n:%s}:#{%s},", f, f)
	}

	return b.String()
}
