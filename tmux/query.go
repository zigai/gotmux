package tmux

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/zigai/gotmux/internal/schema"
	"github.com/zigai/gotmux/internal/wire"
)

const retryBackoff = 5 * time.Millisecond

// Format is an explicit tmux format expression string (such as "#{pane_id}").
type Format string

// QueryOptions configures server-side filtering and extra field extraction for queries.
type QueryOptions struct {
	// Filter is a tmux format condition passed via -f. If empty, all matches are returned.
	Filter Format

	// ExtraFields specifies additional format variable names captured into [rawRecord.Raw].
	ExtraFields []string
}

func queryFields(base []string, extra []string) ([]string, error) {
	out := append([]string{}, base...)

	seen := map[string]bool{}
	for _, s := range out {
		seen[s] = true
	}

	for _, f := range extra {
		if !validFormatName(f) {
			return nil, invalid("format field name")
		}

		if seen[f] {
			continue
		}

		seen[f] = true
		out = append(out, f)
	}

	return out, nil
}

func validFormatName(s string) bool {
	if s == "" {
		return false
	}

	for i := range len(s) {
		b := s[i]
		if (b < 'a' || b > 'z') && (b < 'A' || b > 'Z') && (b < '0' || b > '9') && b != '_' && b != '-' && b != '@' {
			return false
		}
	}

	return true
}

// numericID parses the numeric portion of a decoder-validated tmux ID (e.g. $1, @2, %3).
func numericID(s string) uint64 {
	n, _ := strconv.ParseUint(s[1:], 10, 32)

	return n
}

// Probe verifies communication with the tmux daemon on this server's endpoint and returns
// its verified runtime identity ([ServerIdentity]) and reported [Version].
//
// Unlike [Server.Version], Probe requires an active running daemon and fails with [ErrNoServer]
// if no daemon is listening. It does NOT start a daemon automatically.
func (s *Server) Probe(ctx context.Context) (ServerInfo, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return ServerInfo{}, opError("Probe", err)
	}
	defer op.close()

	v, err := s.probe(opCtx, op)

	return v, opError("Probe", err)
}

func (s *Server) probe(ctx context.Context, op *operation) (ServerInfo, error) {
	wrapErr := func(err error) (ServerInfo, error) {
		return ServerInfo{}, &discoveryError{Err: err}
	}

	fields := schema.Identity
	p := recordsPlan(command("display-message", "-p", wire.RecordFormat(fields)))

	r, err := s.execute(ctx, op, p, nil, nil)
	if err != nil {
		return wrapErr(err)
	}

	rows, err := parseRaw(r.Stdout, fields, "server")
	if err != nil {
		return wrapErr(afterError("Probe", err))
	}

	if len(rows) != 1 {
		return wrapErr(afterError("Probe", decodeError("server", "record count", wire.ErrRecord)))
	}

	d := &recordDecoder{kind: "server", raw: rows[0], err: nil}
	id := s.decodeIdentity(d)

	v := ParseVersion(d.str("version"))
	if d.err != nil {
		return wrapErr(afterError("Probe", d.err))
	}

	if err := supportedVersion(v); err != nil {
		return wrapErr(err)
	}

	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	return ServerInfo{rawRecord: rawRecord{raw: rows[0]}, Identity: id, Version: v}, nil
}

func listCommandAndArgs(kind ObjectKind, target string) (string, []string, error) {
	switch kind {
	case SessionKind:
		return "list-sessions", nil, nil
	case WindowKind:
		if target == "" {
			return "list-windows", []string{"-a"}, nil
		}

		return "list-windows", nil, nil
	case PaneKind:
		if target == "" {
			return "list-panes", []string{"-a"}, nil
		}

		return "list-panes", nil, nil
	case ClientKind:
		return "list-clients", nil, nil
	case LinkKind:
		return "", nil, invalid("object kind")
	default:
		return "", nil, invalid("object kind")
	}
}

func (s *Server) parseOrRetry(ctx context.Context, op *operation, p plan, g *guard, r Result, fields []string, kind string) ([]map[string]string, error) {
	rows, err := parseRaw(r.Stdout, fields, kind)
	if err == nil || !errors.Is(err, wire.ErrRecord) {
		return rows, err
	}

	time.Sleep(retryBackoff)

	r2, err2 := s.execute(ctx, op, p, g, nil)
	if err2 != nil {
		return nil, err
	}

	rows2, err3 := parseRaw(r2.Stdout, fields, kind)
	if err3 != nil {
		return nil, err
	}

	return rows2, nil
}

func (s *Server) listRaw(ctx context.Context, op *operation, kind ObjectKind, expected ServerIdentity, opts QueryOptions, target string) ([]map[string]string, error) {
	fields, err := queryFields(fieldsFor(kind), opts.ExtraFields)
	if err != nil {
		return nil, err
	}

	name, args, err := listCommandAndArgs(kind, target)
	if err != nil {
		return nil, err
	}

	if target != "" {
		args = append(args, "-t", target)
	}

	if opts.Filter != "" {
		if !wire.ValidString(string(opts.Filter)) {
			return nil, invalid("filter")
		}

		args = append(args, "-f", string(opts.Filter))
	}

	args = append(args, "-F", wire.RecordFormat(fields))
	p := recordsPlan(command(name, args...))
	g := newGuard(expected)

	r, err := s.execute(ctx, op, p, g, nil)
	if err != nil {
		return nil, err
	}

	rows, err := s.parseOrRetry(ctx, op, p, g, r, fields, string(kind))
	if err != nil {
		return nil, afterError(name, err)
	}

	return rows, nil
}

// Sessions queries all active sessions on the daemon, returning their point-in-time
// metadata sorted by numeric session ID.
func (s *Server) Sessions(ctx context.Context) ([]SessionInfo, error) {
	return s.SessionsWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// SessionsWith queries sessions matching the specified [QueryOptions] filter and extracts
// any additional requested format fields into [rawRecord.Raw].
func (s *Server) SessionsWith(ctx context.Context, opts QueryOptions) ([]SessionInfo, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Sessions", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Sessions", err)
	}

	out, err := s.sessions(opCtx, op, info.Identity, opts)

	return out, opError("Sessions", err)
}

func (s *Server) sessions(ctx context.Context, op *operation, id ServerIdentity, opts QueryOptions) ([]SessionInfo, error) {
	rows, err := s.listRaw(ctx, op, SessionKind, id, opts, "")
	if err != nil {
		return nil, err
	}

	out := make([]SessionInfo, 0, len(rows))
	for _, m := range rows {
		v, err := s.decodeSession(m, &id)
		if err != nil {
			return nil, afterError("Sessions", err)
		}

		out = append(out, v)
	}

	sort.Slice(out, func(i, j int) bool { return numericID(string(out[i].ID)) < numericID(string(out[j].ID)) })

	return out, nil
}

// Windows queries all windows across all sessions on the daemon, returning deduplicated
// window metadata sorted by numeric window ID.
//
// In tmux, "list-windows -a" emits one record per window link slot: if window @1 is linked
// into three sessions, tmux outputs three lines. Windows deduplicates by [WindowID] so each
// shared window is returned exactly once. To inspect per-session links, use [Session.Windows]
// or [Window.Links].
func (s *Server) Windows(ctx context.Context) ([]WindowInfo, error) {
	return s.WindowsWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// WindowsWith applies a format filter and fetches additional fields.
func (s *Server) WindowsWith(ctx context.Context, opts QueryOptions) ([]WindowInfo, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Windows", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Windows", err)
	}

	w, _, err := s.windowsWith(opCtx, op, info.Identity, opts, "")

	return w, opError("Windows", err)
}

func (s *Server) windows(ctx context.Context, op *operation, id ServerIdentity, target string) ([]WindowInfo, []WindowLinkInfo, error) {
	return s.windowsWith(ctx, op, id, QueryOptions{Filter: "", ExtraFields: nil}, target)
}

func (s *Server) windowsWith(ctx context.Context, op *operation, id ServerIdentity, opts QueryOptions, target string) ([]WindowInfo, []WindowLinkInfo, error) {
	rows, err := s.listRaw(ctx, op, WindowKind, id, opts, target)
	if err != nil {
		return nil, nil, err
	}

	out := make([]WindowInfo, 0, len(rows))
	links := make([]WindowLinkInfo, 0, len(rows))
	seen := map[WindowID]bool{}

	for _, m := range rows {
		v, l, err := s.decodeWindow(m, &id)
		if err != nil {
			return nil, nil, afterError("Windows", err)
		}

		if !seen[v.ID] {
			out = append(out, v)
			seen[v.ID] = true
		}

		links = append(links, l)
	}

	sort.Slice(out, func(i, j int) bool { return numericID(string(out[i].ID)) < numericID(string(out[j].ID)) })
	sort.Slice(links, func(i, j int) bool {
		if links[i].SessionID == links[j].SessionID {
			return links[i].Index < links[j].Index
		}

		return numericID(string(links[i].SessionID)) < numericID(string(links[j].SessionID))
	})

	return out, links, nil
}

// Panes queries all panes across all windows on the daemon, returning their point-in-time
// metadata sorted by numeric pane ID.
func (s *Server) Panes(ctx context.Context) ([]PaneInfo, error) {
	return s.PanesWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// PanesWith queries panes matching the specified [QueryOptions] filter and extracts any
// additional requested format fields into [rawRecord.Raw].
func (s *Server) PanesWith(ctx context.Context, opts QueryOptions) ([]PaneInfo, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Panes", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Panes", err)
	}

	out, err := s.panes(opCtx, op, info.Identity, opts, "")

	return out, opError("Panes", err)
}

func (s *Server) panes(ctx context.Context, op *operation, id ServerIdentity, opts QueryOptions, target string) ([]PaneInfo, error) {
	rows, err := s.listRaw(ctx, op, PaneKind, id, opts, target)
	if err != nil {
		return nil, err
	}

	out := make([]PaneInfo, 0, len(rows))

	seen := make(map[PaneID]bool, len(rows))
	for _, m := range rows {
		v, err := s.decodePane(m, &id)
		if err != nil {
			return nil, afterError("Panes", err)
		}

		// list-panes -a repeats panes for windows linked into multiple sessions.
		if !seen[v.ID] {
			seen[v.ID] = true
			out = append(out, v)
		}
	}

	sort.Slice(out, func(i, j int) bool { return numericID(string(out[i].ID)) < numericID(string(out[j].ID)) })

	return out, nil
}

// Clients queries all client terminals currently connected to the daemon, returning
// their point-in-time metadata sorted by client terminal name.
func (s *Server) Clients(ctx context.Context) ([]ClientInfo, error) {
	return s.ClientsWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// ClientsWith applies a format filter and fetches additional fields.
func (s *Server) ClientsWith(ctx context.Context, opts QueryOptions) ([]ClientInfo, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Clients", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Clients", err)
	}

	out, err := s.clientsWith(opCtx, op, info.Identity, opts)

	return out, opError("Clients", err)
}

func (s *Server) clients(ctx context.Context, op *operation, id ServerIdentity) ([]ClientInfo, error) {
	return s.clientsWith(ctx, op, id, QueryOptions{Filter: "", ExtraFields: nil})
}

func (s *Server) clientsWith(ctx context.Context, op *operation, id ServerIdentity, opts QueryOptions) ([]ClientInfo, error) {
	rows, err := s.listRaw(ctx, op, ClientKind, id, opts, "")
	if err != nil {
		return nil, err
	}

	out := make([]ClientInfo, 0, len(rows))
	for _, m := range rows {
		v, err := s.decodeClient(m, &id)
		if err != nil {
			return nil, afterError("Clients", err)
		}

		out = append(out, v)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

func (s *Server) lookup(ctx context.Context, kind ObjectKind, id string) (map[string]string, error) {
	if kind == SessionKind && !SessionID(id).Valid() || kind == WindowKind && !WindowID(id).Valid() || kind == PaneKind && !PaneID(id).Valid() {
		return nil, invalid("object ID")
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, err
	}

	return s.inspect(opCtx, op, kind, id, newGuard(info.Identity), QueryOptions{Filter: "", ExtraFields: nil})
}

func (s *Server) inspect(ctx context.Context, op *operation, kind ObjectKind, target string, g *guard, opts QueryOptions) (map[string]string, error) {
	fields, err := queryFields(fieldsFor(kind), opts.ExtraFields)
	if err != nil {
		return nil, err
	}

	args := []string{"-p"}
	if kind == ClientKind {
		args = append(args, "-c", target)
	} else {
		args = append(args, "-t", target)
	}

	args = append(args, wire.RecordFormat(fields))

	p := recordsPlan(command("display-message", args...))

	r, err := s.execute(ctx, op, p, g, nil)
	if err != nil {
		return nil, err
	}

	rows, err := s.parseOrRetry(ctx, op, p, g, r, fields, string(kind))
	if err != nil {
		return nil, afterError("Info", err)
	}

	if len(rows) != 1 {
		return nil, afterError("Info", decodeError(string(kind), "record count", wire.ErrRecord))
	}

	// display-message succeeds with empty object fields when its target vanished.
	if (kind == SessionKind || kind == WindowKind || kind == PaneKind) && rows[0][string(kind)+"_id"] == "" {
		return nil, opError("Info", ErrNotFound)
	}

	return rows[0], nil
}

// Session looks up a session by its canonical ID (e.g. "$0"), verifies that it exists
// on the daemon, and returns a [Session] handle retaining daemon origin provenance.
// Returns [ErrNotFound] if no session with that ID exists.
func (s *Server) Session(ctx context.Context, id SessionID) (Session, error) {
	m, err := s.lookup(ctx, SessionKind, string(id))
	if err != nil {
		return Session{}, opError("Session", err)
	}

	v, err := s.decodeSession(m, nil)
	if err != nil {
		return Session{}, afterError("Session", err)
	}

	if v.ID != id {
		return Session{}, afterError("Session", decodeError("session", "ID", ErrProtocol))
	}

	return v.Handle(), nil
}

// Window looks up a window by ID and returns a verified [Window] handle with daemon provenance.
func (s *Server) Window(ctx context.Context, id WindowID) (Window, error) {
	m, err := s.lookup(ctx, WindowKind, string(id))
	if err != nil {
		return Window{}, opError("Window", err)
	}

	v, _, err := s.decodeWindow(m, nil)
	if err != nil {
		return Window{}, afterError("Window", err)
	}

	if v.ID != id {
		return Window{}, afterError("Window", decodeError("window", "ID", ErrProtocol))
	}

	return v.Handle(), nil
}

// Pane looks up a pane by ID and returns a verified [Pane] handle with daemon provenance.
func (s *Server) Pane(ctx context.Context, id PaneID) (Pane, error) {
	m, err := s.lookup(ctx, PaneKind, string(id))
	if err != nil {
		return Pane{}, opError("Pane", err)
	}

	v, err := s.decodePane(m, nil)
	if err != nil {
		return Pane{}, afterError("Pane", err)
	}

	if v.ID != id {
		return Pane{}, afterError("Pane", decodeError("pane", "ID", ErrProtocol))
	}

	return v.Handle(), nil
}

// PaneHandle constructs a [Pane] handle for a known pane ID without performing a server round-trip.
// Syntax is validated (e.g. "%0"), while daemon provenance is deferred until an operation executes.
func (s *Server) PaneHandle(id PaneID) (Pane, error) {
	if s == nil || s.runner == nil {
		return Pane{}, opError("PaneHandle", ErrInvalidHandle)
	}

	if !id.Valid() {
		return Pane{}, opError("PaneHandle", invalid("pane ID"))
	}

	return Pane{h: s.unprobedHandle(string(id), PaneKind)}, nil
}

// SessionHandle constructs a [Session] handle for a known session ID without performing a server round-trip.
func (s *Server) SessionHandle(id SessionID) (Session, error) {
	if s == nil || s.runner == nil {
		return Session{}, opError("SessionHandle", ErrInvalidHandle)
	}

	if !id.Valid() {
		return Session{}, opError("SessionHandle", invalid("session ID"))
	}

	return Session{h: s.unprobedHandle(string(id), SessionKind)}, nil
}

// WindowHandle constructs a [Window] handle for a known window ID without performing a server round-trip.
func (s *Server) WindowHandle(id WindowID) (Window, error) {
	if s == nil || s.runner == nil {
		return Window{}, opError("WindowHandle", ErrInvalidHandle)
	}

	if !id.Valid() {
		return Window{}, opError("WindowHandle", invalid("window ID"))
	}

	return Window{h: s.unprobedHandle(string(id), WindowKind)}, nil
}

// FindSession locates a session by its exact human-readable name.
//
// tmux's native command targets perform prefix matching (e.g. target "dev" will match
// a session named "development"), which easily causes accidental mutations.
// FindSession avoids this danger by listing all sessions and requiring an exact match.
// Returns [ErrNotFound] if no match is found, or [ErrAmbiguousTarget] if multiple sessions
// share the exact same name.
func (s *Server) FindSession(ctx context.Context, exactName string) (Session, error) {
	if exactName == "" || !wire.ValidString(exactName) {
		return Session{}, opError("FindSession", invalid("name"))
	}

	sessions, err := s.Sessions(ctx)
	if err != nil {
		return Session{}, opError("FindSession", err)
	}

	var found Session

	for _, v := range sessions {
		if v.Name == exactName {
			if found.Valid() {
				return Session{}, opError("FindSession", ErrAmbiguousTarget)
			}

			found = v.Handle()
		}
	}

	if !found.Valid() {
		return Session{}, opError("FindSession", ErrNotFound)
	}

	return found, nil
}

// Client looks up an attached client terminal by its name (e.g. "/dev/pts/1"), verifies
// that it is currently connected, and returns a [Client] handle.
// Returns [ErrNotFound] if the client is not connected.
func (s *Server) Client(ctx context.Context, name ClientName) (Client, error) {
	if !name.Valid() {
		return Client{}, opError("Client", invalid("client name"))
	}

	clients, err := s.Clients(ctx)
	if err != nil {
		return Client{}, opError("Client", err)
	}

	for _, v := range clients {
		if v.Name == name {
			return v.Handle(), nil
		}
	}

	return Client{}, opError("Client", ErrNotFound)
}

func (h handle) inspectWith(ctx context.Context, opts QueryOptions) (map[string]string, error) {
	if err := h.check(); err != nil {
		return nil, err
	}

	opCtx, op, err := h.server.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer op.close()

	return h.server.inspect(opCtx, op, h.kind, h.id, h.guard(), opts)
}

// Info queries the answering daemon for the current point-in-time metadata of this session.
// It validates daemon identity via guards to ensure the server has not restarted.
func (s Session) Info(ctx context.Context) (SessionInfo, error) {
	return s.InfoWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// InfoWith queries the answering daemon for the metadata of this session with custom query options.
func (s Session) InfoWith(ctx context.Context, opts QueryOptions) (SessionInfo, error) {
	m, err := s.h.inspectWith(ctx, opts)
	if err != nil {
		return SessionInfo{}, opError("Session.Info", err)
	}

	v, err := s.h.server.decodeSession(m, &s.h.origin)
	if err != nil {
		return SessionInfo{}, afterError("Session.Info", err)
	}

	if v.ID != s.ID() {
		return SessionInfo{}, afterError("Session.Info", ErrProtocol)
	}

	return v, nil
}

// Info queries the answering daemon for the current point-in-time metadata of this window.
func (w Window) Info(ctx context.Context) (WindowInfo, error) {
	return w.InfoWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// InfoWith queries the answering daemon for the metadata of this window with custom query options.
func (w Window) InfoWith(ctx context.Context, opts QueryOptions) (WindowInfo, error) {
	m, err := w.h.inspectWith(ctx, opts)
	if err != nil {
		return WindowInfo{}, opError("Window.Info", err)
	}

	v, _, err := w.h.server.decodeWindow(m, &w.h.origin)
	if err != nil {
		return WindowInfo{}, afterError("Window.Info", err)
	}

	if v.ID != w.ID() {
		return WindowInfo{}, afterError("Window.Info", ErrProtocol)
	}

	return v, nil
}

// Info queries the answering daemon for the current point-in-time metadata of this pane.
func (p Pane) Info(ctx context.Context) (PaneInfo, error) {
	return p.InfoWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// InfoWith queries the answering daemon for the metadata of this pane with custom query options.
func (p Pane) InfoWith(ctx context.Context, opts QueryOptions) (PaneInfo, error) {
	m, err := p.h.inspectWith(ctx, opts)
	if err != nil {
		return PaneInfo{}, opError("Pane.Info", err)
	}

	v, err := p.h.server.decodePane(m, &p.h.origin)
	if err != nil {
		return PaneInfo{}, afterError("Pane.Info", err)
	}

	if v.ID != p.ID() {
		return PaneInfo{}, afterError("Pane.Info", ErrProtocol)
	}

	return v, nil
}

// Info queries the answering daemon for the current point-in-time metadata of this client.
func (c Client) Info(ctx context.Context) (ClientInfo, error) {
	return c.InfoWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// InfoWith queries the answering daemon for the metadata of this client with custom query options.
func (c Client) InfoWith(ctx context.Context, opts QueryOptions) (ClientInfo, error) {
	m, err := c.h.inspectWith(ctx, opts)
	if err != nil {
		return ClientInfo{}, opError("Client.Info", err)
	}

	v, err := c.h.server.decodeClient(m, &c.h.origin)
	if err != nil {
		return ClientInfo{}, afterError("Client.Info", err)
	}

	if v.Name != c.Name() {
		return ClientInfo{}, afterError("Client.Info", ErrProtocol)
	}

	return v, nil
}

// Info queries the answering daemon for the metadata of this specific window link slot.
// Asserts link guards to verify the window at this session index has not changed or been unlinked.
func (l WindowLink) Info(ctx context.Context) (WindowLinkInfo, error) {
	return l.InfoWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// InfoWith queries the answering daemon for the metadata of this window link slot with custom query options.
func (l WindowLink) InfoWith(ctx context.Context, opts QueryOptions) (WindowLinkInfo, error) {
	if err := l.check(); err != nil {
		return WindowLinkInfo{}, opError("WindowLink.Info", err)
	}

	opCtx, op, err := l.h.server.begin(ctx)
	if err != nil {
		return WindowLinkInfo{}, opError("WindowLink.Info", err)
	}
	defer op.close()

	m, err := l.h.server.inspect(opCtx, op, WindowKind, l.target(), l.guard(), opts)
	if err != nil {
		return WindowLinkInfo{}, opError("WindowLink.Info", err)
	}

	_, v, err := l.h.server.decodeWindow(m, &l.h.origin)
	if err != nil {
		return WindowLinkInfo{}, afterError("WindowLink.Info", err)
	}

	if v.SessionID != l.session || v.Index != l.index || v.WindowID != WindowID(l.h.id) {
		return WindowLinkInfo{}, afterError("WindowLink.Info", ErrLinkChanged)
	}

	return v, nil
}

// Window looks up and returns the parent [Window] handle containing this pane.
func (p Pane) Window(ctx context.Context) (Window, error) {
	v, err := p.Info(ctx)
	if err != nil {
		return Window{}, opError("Pane.Window", err)
	}

	return Window{h: p.h.withOrigin(string(v.WindowID), WindowKind)}, nil
}

// Windows returns all [WindowLink] handles linked into this session, ordered by slot index.
func (s Session) Windows(ctx context.Context) ([]WindowLink, error) {
	if err := s.h.check(); err != nil {
		return nil, opError("Session.Windows", err)
	}

	opCtx, op, err := s.h.server.begin(ctx)
	if err != nil {
		return nil, opError("Session.Windows", err)
	}
	defer op.close()

	_, links, err := s.h.server.windows(opCtx, op, s.h.origin, s.h.id)
	if err != nil {
		return nil, opError("Session.Windows", err)
	}

	out := make([]WindowLink, 0, len(links))
	for _, l := range links {
		out = append(out, l.Handle())
	}

	return out, nil
}

// WindowInfos queries all window metadata and link slots for this session in a single round-trip.
func (s Session) WindowInfos(ctx context.Context) ([]WindowInfo, []WindowLinkInfo, error) {
	if err := s.h.check(); err != nil {
		return nil, nil, opError("Session.WindowInfos", err)
	}

	opCtx, op, err := s.h.server.begin(ctx)
	if err != nil {
		return nil, nil, opError("Session.WindowInfos", err)
	}
	defer op.close()

	windows, links, err := s.h.server.windows(opCtx, op, s.h.origin, s.h.id)
	if err != nil {
		return nil, nil, opError("Session.WindowInfos", err)
	}

	return windows, links, nil
}

// Links queries all session slots across the entire daemon where this window is currently linked.
// Returns [ErrNotFound] if the window has been closed or unlinked from all sessions.
func (w Window) Links(ctx context.Context) ([]WindowLink, error) {
	if err := w.h.check(); err != nil {
		return nil, opError("Window.Links", err)
	}

	opCtx, op, err := w.h.server.begin(ctx)
	if err != nil {
		return nil, opError("Window.Links", err)
	}
	defer op.close()

	_, links, err := w.h.server.windows(opCtx, op, w.h.origin, "")
	if err != nil {
		return nil, opError("Window.Links", err)
	}

	out := []WindowLink{}

	for _, l := range links {
		if l.WindowID == w.ID() {
			out = append(out, l.Handle())
		}
	}

	if len(out) == 0 {
		return nil, opError("Window.Links", ErrNotFound)
	}

	return out, nil
}

// Panes queries all panes currently belonging to this window, sorted by numeric pane ID.
func (w Window) Panes(ctx context.Context) ([]PaneInfo, error) {
	return w.PanesWith(ctx, QueryOptions{Filter: "", ExtraFields: nil})
}

// PanesWith filters panes in this window and fetches additional fields.
func (w Window) PanesWith(ctx context.Context, opts QueryOptions) ([]PaneInfo, error) {
	if err := w.h.check(); err != nil {
		return nil, opError("Window.Panes", err)
	}

	opCtx, op, err := w.h.server.begin(ctx)
	if err != nil {
		return nil, opError("Window.Panes", err)
	}

	defer op.close()

	p, err := w.h.server.panes(opCtx, op, w.h.origin, opts, w.h.id)
	if err != nil {
		return nil, opError("Window.Panes", err)
	}

	return p, nil
}

// ActivePane returns a handle to the currently focused pane inside this window.
// Returns [ErrNotFound] if the window contains no active pane.
func (w Window) ActivePane(ctx context.Context) (Pane, error) {
	panes, err := w.Panes(ctx)
	if err != nil {
		return Pane{}, opError("Window.ActivePane", err)
	}

	for _, p := range panes {
		if p.Active {
			return p.Handle(), nil
		}
	}

	return Pane{h: zeroHandle}, opError("Window.ActivePane", ErrNotFound)
}

// Format evaluates an explicit tmux format expression in the target pane's context
// and returns the expanded output bytes without trimming.
// Format evaluates an expression in this pane's context.
func (p Pane) Format(ctx context.Context, expr Format) ([]byte, error) { return p.h.format(ctx, expr) }

// Format evaluates an expression in this session's context.
func (s Session) Format(ctx context.Context, expr Format) ([]byte, error) {
	return s.h.format(ctx, expr)
}

// Format evaluates an expression in this window's context.
func (w Window) Format(ctx context.Context, expr Format) ([]byte, error) {
	return w.h.format(ctx, expr)
}

// Format evaluates an expression in this client's context.
func (c Client) Format(ctx context.Context, expr Format) ([]byte, error) {
	return c.h.format(ctx, expr)
}

func (h handle) format(ctx context.Context, expr Format) ([]byte, error) {
	if err := h.check(); err != nil {
		return nil, opError("Format", err)
	}

	if !wire.ValidString(string(expr)) {
		return nil, opError("Format", invalid("format"))
	}

	opCtx, op, err := h.server.begin(ctx)
	if err != nil {
		return nil, opError("Format", err)
	}
	defer op.close()

	targetFlag := "-t"
	if h.kind == ClientKind {
		targetFlag = "-c"
	}

	r, err := h.server.execute(opCtx, op, recordsPlan(command("display-message", "-p", targetFlag, h.id, wire.ExpressionFormat(string(expr)))), h.guard(), nil)
	if err != nil {
		return r.Stdout, opError("Format", err)
	}

	records, err := wire.ParseRecords(r.Stdout, 1)
	if err != nil || len(records) != 1 {
		return nil, afterError("Format", errors.Join(err, wire.ErrRecord))
	}

	return []byte(records[0][0]), nil
}
