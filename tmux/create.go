package tmux

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	PaneBorderSingle PaneBorderLines = "single"
	PaneBorderDouble PaneBorderLines = "double"
	PaneBorderHeavy  PaneBorderLines = "heavy"
	PaneBorderSimple PaneBorderLines = "simple"
	PaneBorderNumber PaneBorderLines = "number"
)

type (
	// PaneBorderLines specifies the border style for pane dividers and floating panes (-B flag).
	PaneBorderLines string

	// NewSessionOptions configures the creation of a new tmux session.
	NewSessionOptions struct {
		// Name is the session name (cannot contain colons or periods). Empty defaults to automatic naming.
		Name string

		// Dir specifies the initial working directory for the session's first window.
		Dir string

		// Window specifies the name for the initial window. Disallowed when joining a Group.
		Window string

		// Program specifies the initial command. Zero value runs the default shell.
		Program Program

		// Env specifies environment variable overrides for the launched initial process.
		// Overrides are applied directly to the process environment via an execution wrapper,
		// not native session environment variables (new-session -e).
		// Overriding environment variables requires an explicit [Exec] or [Shell] Program;
		// using Env with the zero Program{} returns [ErrUnsupported] to prevent ambient PATH corruption.
		Env map[string]string

		// TmuxEnv specifies native tmux session environment variables (-e KEY=VAL).
		// Each entry is emitted as a native -e flag on new-session.
		// Unlike [Env], which wraps program execution and requires explicit [Exec] or [Shell],
		// TmuxEnv sets environment variables directly in tmux and can be used when Program
		// is the zero value (running the default shell with native session environment).
		TmuxEnv map[string]string
		// Size specifies initial window dimensions in character cells.
		Size Size

		// Start controls whether to start a new daemon if one is not running.
		Start StartPolicy

		// Group joins an existing session group. Disallows initial window name or program.
		Group string
	}

	// NewWindowOptions configures the creation of a new window inside an existing session.
	NewWindowOptions struct {
		// Name is the window name (#{window_name}).
		Name string

		// Dir is the working directory for the initial pane.
		Dir string

		// Program specifies the initial command. Zero value runs the default shell.
		Program Program

		// Env specifies environment variable overrides for the launched process via an execution wrapper.
		// Requires explicit [Exec] or [Shell].
		Env map[string]string

		// TmuxEnv specifies native tmux environment variables to set for the new window (-e KEY=VAL).
		TmuxEnv map[string]string

		// Index optionally specifies the slot index in the session.
		// Mutually exclusive with Before and After.
		Index *int

		// Before inserts the new window before the target window (-b flag).
		// Mutually exclusive with Index and After.
		Before bool

		// After inserts the new window after the target window (-a flag).
		// Mutually exclusive with Index and Before.
		After bool

		// Select controls whether the new window gains focus immediately (default false).
		Select bool
	}

	// SplitOptions configures splitting an existing pane into two panes.
	SplitOptions struct {
		// Direction specifies vertical (top/bottom) or horizontal (side-by-side) split.
		Direction Direction

		// Size specifies cell count or percentage. Zero splits available space evenly.
		Size SplitSize

		// Dir is the working directory for the new pane.
		Dir string

		// Program specifies the initial process. Zero value runs the default shell.
		Program Program

		// Env specifies environment variable overrides for the launched process via an execution wrapper.
		// Requires explicit [Exec] or [Shell].
		Env map[string]string

		// TmuxEnv specifies native tmux environment variables to set for the new pane (-e KEY=VAL).
		TmuxEnv map[string]string

		// Select controls whether the new pane gains focus immediately.
		Select bool

		// Before places the new pane before (above or left of) the target pane (-b flag).
		Before bool

		// FullSize splits across the full window span (-f flag) rather than just the target pane.
		FullSize bool

		// KillTarget kills the target pane instead of splitting (-k flag).
		KillTarget bool

		// Zoom keeps the window zoomed or zooms the new pane (-Z flag).
		Zoom bool

		// Title sets the initial title for the pane (-T flag).
		Title string

		// BorderLines specifies the border style for the pane (-B flag).
		BorderLines PaneBorderLines

		// Style specifies the pane style (-s flag).
		Style string

		// ActiveBorderStyle specifies the active border style (-S flag).
		ActiveBorderStyle string

		// InactiveBorderStyle specifies the inactive border style (-R flag).
		InactiveBorderStyle string

		// Message specifies an optional message to display in the pane (-m flag).
		Message string
	}

	// NewPaneOptions configures creating a new (potentially floating or modal) pane via tmux new-pane.
	NewPaneOptions struct {
		// Width specifies the pane width in character cells or percentage (e.g. "50%" or "40").
		Width string

		// Height specifies the pane height in character cells or percentage (e.g. "50%" or "15").
		Height string

		// X specifies the horizontal position in character cells or percentage (e.g. "10" or "10%").
		X string

		// Y specifies the vertical position in character cells or percentage (e.g. "5" or "5%").
		Y string

		// Modal creates a modal pane blocking input to other panes until dismissed (-O flag).
		Modal bool

		// Dir specifies the initial working directory (-c flag).
		Dir string

		// Program specifies the initial process. Zero value runs the default shell.
		Program Program

		// Env specifies environment variable overrides for the launched process via an execution wrapper.
		// Requires explicit [Exec] or [Shell].
		Env map[string]string

		// TmuxEnv specifies native tmux environment variables to set (-e KEY=VAL).
		TmuxEnv map[string]string

		// Select controls whether the new pane gains focus immediately.
		Select bool

		// Zoom keeps the window zoomed or zooms the new pane (-Z flag).
		Zoom bool

		// Title sets the initial title for the pane (-T flag).
		Title string

		// BorderLines specifies the border style for the pane (-B flag).
		BorderLines PaneBorderLines

		// Style specifies the pane style (-s flag).
		Style string

		// ActiveBorderStyle specifies the active border style (-S flag).
		ActiveBorderStyle string

		// InactiveBorderStyle specifies the inactive border style (-R flag).
		InactiveBorderStyle string

		// FloatOverZoom permits floating over zoomed panes (-A flag).
		FloatOverZoom bool

		// CloseOnClick closes the modal pane on mouse click outside (-C flag).
		CloseOnClick bool

		// CaptureAllKeys routes all keys to the modal pane (-K flag).
		CaptureAllKeys bool
	}

	startGuard struct {
		guard      *guard
		allowStart bool
	}
)

// Valid reports whether the pane border style is a recognized tmux pane-border-lines value.
func (b PaneBorderLines) Valid() bool {
	switch b {
	case PaneBorderSingle, PaneBorderDouble, PaneBorderHeavy, PaneBorderSimple, PaneBorderNumber:
		return true
	default:
		return false
	}
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

	args, err = s.exactGroupArgs(opCtx, op, sg, args, opts.Group)
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

func (s *Server) exactGroupArgs(ctx context.Context, op *operation, sg startGuard, args []string, name string) ([]string, error) {
	if name == "" {
		return args, nil
	}

	if sg.guard == nil {
		return nil, ErrNotFound
	}

	target, err := s.exactGroupTarget(ctx, op, sg.guard.identity, name)
	if err != nil {
		return nil, err
	}

	i := slices.Index(args, "-t")
	if i < 0 || i+1 >= len(args) {
		return nil, ErrProtocol
	}

	args[i+1] = target

	return args, nil
}

func (s *Server) exactGroupTarget(ctx context.Context, op *operation, id ServerIdentity, name string) (string, error) {
	sessions, err := s.sessions(ctx, op, id, QueryOptions{Filter: "", ExtraFields: nil})
	if err != nil {
		return "", err
	}

	var sessionTarget string

	for _, session := range sessions {
		if group, ok := session.Group.Get(); ok && group == name {
			return string(session.ID), nil
		}

		if session.Name == name {
			sessionTarget = string(session.ID)
		}
	}

	if sessionTarget == "" {
		return "", ErrNotFound
	}

	return sessionTarget, nil
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

	_, link, err := s.h.server.decodeWindow(rows[0], s.h.expectedOrigin())
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

	v, err := p.h.server.decodePane(rows[0], p.h.expectedOrigin())
	if err != nil {
		return Pane{}, afterError("Split", err, recoverCreated(r.Stdout, PaneKind)...)
	}

	if err = opCtx.Err(); err != nil {
		return v.Handle(), afterError("Split", err, createdFromHandle(v.h))
	}

	return v.Handle(), nil
}

func createNewPane(ctx context.Context, h handle, opName string, targetID string, opts NewPaneOptions) (Pane, error) {
	if err := h.check(); err != nil {
		return Pane{}, opError(opName, err)
	}

	opCtx, op, err := h.server.begin(ctx)
	if err != nil {
		return Pane{}, opError(opName, err)
	}
	defer op.close()

	args, err := newPaneArgs(targetID, opts)
	if err != nil {
		return Pane{}, opError(opName, err)
	}

	r, err := h.server.execute(opCtx, op, recordsPlan(command("new-pane", args...)), h.guard(), nil)
	if err != nil {
		return Pane{}, creationError(opName, err, r.Stdout, PaneKind)
	}

	rows, err := parseRaw(r.Stdout, fieldsFor(PaneKind), "pane")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = decodeError("pane", "record count", wire.ErrRecord)
		}

		return Pane{}, afterError(opName, err, recoverCreated(r.Stdout, PaneKind)...)
	}

	v, err := h.server.decodePane(rows[0], h.expectedOrigin())
	if err != nil {
		return Pane{}, afterError(opName, err, recoverCreated(r.Stdout, PaneKind)...)
	}

	if err = opCtx.Err(); err != nil {
		return v.Handle(), afterError(opName, err, createdFromHandle(v.h))
	}

	return v.Handle(), nil
}

// NewPane creates a new (potentially floating or modal) pane targeting this window (new-pane).
func (w Window) NewPane(ctx context.Context, opts NewPaneOptions) (Pane, error) {
	return createNewPane(ctx, w.h, "NewPane", w.h.id, opts)
}

// NewPane creates a new (potentially floating or modal) pane targeting this pane (new-pane).
func (p Pane) NewPane(ctx context.Context, opts NewPaneOptions) (Pane, error) {
	return createNewPane(ctx, p.h, "NewPane", p.h.id, opts)
}

func creationError(name string, err error, data []byte, kind ObjectKind) error {
	outcome := outcomeOf(err)

	outcome.Created = append(outcome.Created, recoverCreated(data, kind)...)
	if name == "NewSession" && errors.Is(err, ErrAlreadyExists) && len(data) == 0 && len(outcome.Created) == 0 && rejectionComplete(err) {
		outcome.Effect = Rejected
	}

	return &OperationError{Operation: name, Outcome: outcome, Err: err}
}

func rejectionComplete(err error) bool {
	cmd, ok := errors.AsType[*CommandError](err)

	return ok && cmd.Timeout == NoTimeout && !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrOutputLimit) &&
		!errors.Is(err, ErrProtocol) && !errors.Is(err, ErrClosed) &&
		!errors.Is(err, ErrServerChanged) && !errors.Is(err, ErrNoServer)
}

func newSessionArgs(opts NewSessionOptions) ([]string, error) {
	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	envArgs, err := tmuxEnvArgs(opts.TmuxEnv)
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

		args = append(args, "-t", opts.Group)
	}

	args = append(args, sessionSizeArgs(opts.Size)...)
	args = append(args, envArgs...)
	args = append(args, extra...)

	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return args, nil
}

func (s *Server) parseCreatedSession(data []byte, g *guard) (Session, error) {
	rows, err := parseRaw(data, fieldsFor(SessionKind), "session")
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = decodeError("session", "record count", wire.ErrRecord)
		}

		return Session{}, afterError("NewSession", err, recoverCreated(data, SessionKind)...)
	}

	var expected *ServerIdentity
	if g != nil {
		expected = &g.identity
	}

	sess, err := s.decodeSession(rows[0], expected)
	if err != nil {
		return Session{}, afterError("NewSession", err, recoverCreated(data, SessionKind)...)
	}

	return sess.Handle(), nil
}

func newSessionPlan(args []string, sg startGuard) plan {
	p := recordsPlan(command("new-session", args...))

	p.allowStart = sg.allowStart
	if p.allowStart {
		p.nodes = []wireNode{leaf(command("start-server")), conditionNode(startupVersionCondition(), p.nodes, []wireNode{markerNode(unsupportedStartupVersion)}, "")}
	}

	return p
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
	case errors.Is(err, ErrNoServer) && start == ExistingOnly:
		return startGuard{guard: nil, allowStart: false}, ErrNoServer
	default:
		return startGuard{guard: nil, allowStart: false}, err
	}
}

func newWindowArgs(sessionID string, opts NewWindowOptions) ([]string, error) {
	target, err := newWindowTarget(sessionID, opts)
	if err != nil {
		return nil, err
	}

	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	envArgs, err := tmuxEnvArgs(opts.TmuxEnv)
	if err != nil {
		return nil, err
	}

	args := []string{"-P", "-F", wire.RecordFormat(fieldsFor(WindowKind)), "-t", target}
	if opts.Before {
		args = append(args, "-b")
	}

	if opts.After {
		args = append(args, "-a")
	}

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

	args = append(args, envArgs...)
	args = append(args, extra...)

	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return args, nil
}

func splitLayoutFlags(opts SplitOptions) []string {
	var flags []string
	if opts.Direction == Horizontal {
		flags = append(flags, "-h")
	} else {
		flags = append(flags, "-v")
	}

	if !opts.Select {
		flags = append(flags, "-d")
	}

	if opts.Before {
		flags = append(flags, "-b")
	}

	if opts.FullSize {
		flags = append(flags, "-f")
	}

	if opts.KillTarget {
		flags = append(flags, "-k")
	}

	if opts.Zoom {
		flags = append(flags, "-Z")
	}

	return flags
}

func splitTitleBorderFlags(opts SplitOptions) ([]string, error) {
	var flags []string

	if opts.Title != "" {
		if !wire.ValidString(opts.Title) {
			return nil, invalid("title")
		}

		flags = append(flags, "-T", opts.Title)
	}

	if opts.BorderLines != "" {
		if !opts.BorderLines.Valid() {
			return nil, invalid("border lines")
		}

		flags = append(flags, "-B", string(opts.BorderLines))
	}

	return flags, nil
}

func splitColorStyleFlags(opts SplitOptions) ([]string, error) {
	var flags []string

	if opts.Style != "" {
		if !wire.ValidString(opts.Style) {
			return nil, invalid("style")
		}

		flags = append(flags, "-s", opts.Style)
	}

	if opts.ActiveBorderStyle != "" {
		if !wire.ValidString(opts.ActiveBorderStyle) {
			return nil, invalid("active border style")
		}

		flags = append(flags, "-S", opts.ActiveBorderStyle)
	}

	if opts.InactiveBorderStyle != "" {
		if !wire.ValidString(opts.InactiveBorderStyle) {
			return nil, invalid("inactive border style")
		}

		flags = append(flags, "-R", opts.InactiveBorderStyle)
	}

	if opts.Message != "" {
		if !wire.ValidString(opts.Message) {
			return nil, invalid("message")
		}

		flags = append(flags, "-m", opts.Message)
	}

	return flags, nil
}

func splitStyleFlags(opts SplitOptions) ([]string, error) {
	tbFlags, err := splitTitleBorderFlags(opts)
	if err != nil {
		return nil, err
	}

	csFlags, err := splitColorStyleFlags(opts)
	if err != nil {
		return nil, err
	}

	return append(tbFlags, csFlags...), nil
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

	envArgs, err := tmuxEnvArgs(opts.TmuxEnv)
	if err != nil {
		return nil, err
	}

	styleArgs, err := splitStyleFlags(opts)
	if err != nil {
		return nil, err
	}

	args := []string{"-P", "-F", splitRecordFormat(fieldsFor(PaneKind)), "-t", paneID}
	args = append(args, splitLayoutFlags(opts)...)
	args = append(args, styleArgs...)
	args = append(args, size...)
	args = append(args, envArgs...)
	args = append(args, extra...)

	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return args, nil
}

func tmuxEnvArgs(env map[string]string) ([]string, error) {
	if len(env) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(env))
	for k, v := range env {
		if !envName(k) || !wire.ValidString(v) {
			return nil, invalid("environment entry")
		}

		keys = append(keys, k)
	}

	slices.Sort(keys)

	const envArgsPerVar = 2

	args := make([]string, 0, len(keys)*envArgsPerVar)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
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
	case ClientKind, WindowLinkKind:
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
			b.WriteString("0:,")
			continue
		}

		fmt.Fprintf(&b, "#{n:%s}:#{%s},", f, f)
	}

	return b.String()
}

func sessionSizeArgs(s Size) []string {
	var args []string

	if s.Width != 0 {
		args = append(args, "-x", strconv.Itoa(s.Width))
	}

	if s.Height != 0 {
		args = append(args, "-y", strconv.Itoa(s.Height))
	}

	return args
}

func newWindowTarget(sessionID string, opts NewWindowOptions) (string, error) {
	placement := 0
	if opts.Index != nil {
		placement++
	}

	if opts.Before {
		placement++
	}

	if opts.After {
		placement++
	}

	if placement > 1 {
		return "", invalid("window placement")
	}

	target := sessionID + ":"

	if opts.Index != nil {
		if *opts.Index < 0 || *opts.Index > 1<<30 {
			return "", invalid("index")
		}

		target += strconv.Itoa(*opts.Index)
	}

	return target, nil
}

func newPaneGeometryFlags(opts NewPaneOptions) ([]string, error) {
	var flags []string

	if opts.Width != "" {
		if !wire.ValidString(opts.Width) {
			return nil, invalid("width")
		}

		flags = append(flags, "-x", opts.Width)
	}

	if opts.Height != "" {
		if !wire.ValidString(opts.Height) {
			return nil, invalid("height")
		}

		flags = append(flags, "-y", opts.Height)
	}

	if opts.X != "" {
		if !wire.ValidString(opts.X) {
			return nil, invalid("x position")
		}

		flags = append(flags, "-X", opts.X)
	}

	if opts.Y != "" {
		if !wire.ValidString(opts.Y) {
			return nil, invalid("y position")
		}

		flags = append(flags, "-Y", opts.Y)
	}

	return flags, nil
}

func newPaneModalFlags(opts NewPaneOptions) []string {
	var flags []string

	if opts.Modal {
		flags = append(flags, "-O")

		if opts.CloseOnClick {
			flags = append(flags, "-C")
		}

		if opts.CaptureAllKeys {
			flags = append(flags, "-K")
		}
	}

	if opts.FloatOverZoom {
		flags = append(flags, "-A")
	}

	if !opts.Select {
		flags = append(flags, "-d")
	}

	if opts.Zoom {
		flags = append(flags, "-Z")
	}

	return flags
}

func newPaneStyleFlags(opts NewPaneOptions) ([]string, error) {
	var flags []string

	if opts.Title != "" {
		if !wire.ValidString(opts.Title) {
			return nil, invalid("title")
		}

		flags = append(flags, "-T", opts.Title)
	}

	if opts.BorderLines != "" {
		if !opts.BorderLines.Valid() {
			return nil, invalid("border lines")
		}

		flags = append(flags, "-B", string(opts.BorderLines))
	}

	if opts.Style != "" {
		if !wire.ValidString(opts.Style) {
			return nil, invalid("style")
		}

		flags = append(flags, "-s", opts.Style)
	}

	if opts.ActiveBorderStyle != "" {
		if !wire.ValidString(opts.ActiveBorderStyle) {
			return nil, invalid("active border style")
		}

		flags = append(flags, "-S", opts.ActiveBorderStyle)
	}

	if opts.InactiveBorderStyle != "" {
		if !wire.ValidString(opts.InactiveBorderStyle) {
			return nil, invalid("inactive border style")
		}

		flags = append(flags, "-R", opts.InactiveBorderStyle)
	}

	return flags, nil
}

func newPaneArgs(targetID string, opts NewPaneOptions) ([]string, error) {
	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	envArgs, err := tmuxEnvArgs(opts.TmuxEnv)
	if err != nil {
		return nil, err
	}

	geoFlags, err := newPaneGeometryFlags(opts)
	if err != nil {
		return nil, err
	}

	styleFlags, err := newPaneStyleFlags(opts)
	if err != nil {
		return nil, err
	}

	args := []string{"-P", "-F", splitRecordFormat(fieldsFor(PaneKind)), "-t", targetID}
	args = append(args, geoFlags...)
	args = append(args, newPaneModalFlags(opts)...)
	args = append(args, styleFlags...)
	args = append(args, envArgs...)
	args = append(args, extra...)

	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return args, nil
}
