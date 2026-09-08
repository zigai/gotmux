package tmux

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"example.com/tmux/internal/codec"
	"example.com/tmux/internal/schema"
)

// Format is an explicit tmux format expression, not literal text or shell code.
type Format string

type QueryOptions struct {
	Filter      Format
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
			return nil, invalid("duplicate format field")
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
	for i := 0; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '@') {
			return false
		}
	}
	return true
}
func numericID(s string) uint64 {
	if len(s) < 2 {
		return 0
	}
	n, _ := strconv.ParseUint(s[1:], 10, 32)
	return n
}

func (s *Server) Probe(ctx context.Context) (ServerInfo, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return ServerInfo{}, opError("Probe", e)
	}
	defer op.close()
	v, e := s.probe(op)
	return v, opError("Probe", e)
}
func (s *Server) probe(op *operation) (info ServerInfo, err error) {
	defer func() {
		if err != nil {
			err = &discoveryError{Err: err}
		}
	}()
	fields := schema.Identity
	p := recordsPlan(command("display-message", "-p", codec.RecordFormat(fields)))
	r, e := s.execute(op, p, nil, nil)
	if e != nil {
		return ServerInfo{}, e
	}
	rows, e := parseRaw(r.Stdout, fields, "server")
	if e != nil {
		return ServerInfo{}, afterError("Probe", e)
	}
	if len(rows) != 1 {
		return ServerInfo{}, afterError("Probe", decodeError("server", "record count", codec.ErrRecord))
	}
	d := &recordDecoder{kind: "server", raw: rows[0]}
	id := s.decodeIdentity(d)
	v := ParseVersion(d.str("version"))
	if d.err != nil {
		return ServerInfo{}, afterError("Probe", d.err)
	}
	if e := supportedVersion(v); e != nil {
		return ServerInfo{}, e
	}
	return ServerInfo{Identity: id, Version: v, rawRecord: rawRecord{raw: rows[0]}}, nil
}

func (s *Server) listRaw(op *operation, kind ObjectKind, expected ServerIdentity, opts QueryOptions, target string) ([]map[string]string, error) {
	fields, e := queryFields(fieldsFor(kind), opts.ExtraFields)
	if e != nil {
		return nil, e
	}
	name := ""
	args := []string{}
	switch kind {
	case SessionKind:
		name = "list-sessions"
	case WindowKind:
		name = "list-windows"
		if target == "" {
			args = append(args, "-a")
		}
	case PaneKind:
		name = "list-panes"
		if target == "" {
			args = append(args, "-a")
		}
	case ClientKind:
		name = "list-clients"
	}
	if name == "" {
		return nil, invalid("record kind")
	}
	if target != "" {
		args = append(args, "-t", target)
	}
	if opts.Filter != "" {
		if !codec.ValidString(string(opts.Filter)) {
			return nil, invalid("filter")
		}
		args = append(args, "-f", string(opts.Filter))
	}
	args = append(args, "-F", codec.RecordFormat(fields))
	r, e := s.execute(op, recordsPlan(command(name, args...)), &guard{identity: expected}, nil)
	if e != nil {
		return nil, e
	}
	rows, e := parseRaw(r.Stdout, fields, string(kind))
	if e != nil {
		return nil, afterError(name, e)
	}
	return rows, nil
}
func (s *Server) Sessions(ctx context.Context) ([]SessionInfo, error) {
	return s.SessionsWith(ctx, QueryOptions{})
}
func (s *Server) SessionsWith(ctx context.Context, opts QueryOptions) ([]SessionInfo, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return nil, opError("Sessions", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return nil, opError("Sessions", e)
	}
	out, e := s.sessions(op, info.Identity, opts)
	return out, opError("Sessions", e)
}
func (s *Server) sessions(op *operation, id ServerIdentity, opts QueryOptions) ([]SessionInfo, error) {
	rows, e := s.listRaw(op, SessionKind, id, opts, "")
	if e != nil {
		return nil, e
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, m := range rows {
		v, e := s.decodeSession(m, &id)
		if e != nil {
			return nil, afterError("Sessions", e)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return numericID(string(out[i].ID)) < numericID(string(out[j].ID)) })
	return out, nil
}
func (s *Server) Windows(ctx context.Context) ([]WindowInfo, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return nil, opError("Windows", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return nil, opError("Windows", e)
	}
	w, _, e := s.windows(op, info.Identity, QueryOptions{}, "")
	return w, opError("Windows", e)
}
func (s *Server) windows(op *operation, id ServerIdentity, opts QueryOptions, target string) ([]WindowInfo, []WindowLinkInfo, error) {
	rows, e := s.listRaw(op, WindowKind, id, opts, target)
	if e != nil {
		return nil, nil, e
	}
	out := make([]WindowInfo, 0, len(rows))
	links := make([]WindowLinkInfo, 0, len(rows))
	seen := map[WindowID]bool{}
	for _, m := range rows {
		v, l, e := s.decodeWindow(m, &id)
		if e != nil {
			return nil, nil, afterError("Windows", e)
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
func (s *Server) Panes(ctx context.Context) ([]PaneInfo, error) {
	return s.PanesWith(ctx, QueryOptions{})
}
func (s *Server) PanesWith(ctx context.Context, opts QueryOptions) ([]PaneInfo, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return nil, opError("Panes", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return nil, opError("Panes", e)
	}
	out, e := s.panes(op, info.Identity, opts, "")
	return out, opError("Panes", e)
}
func (s *Server) panes(op *operation, id ServerIdentity, opts QueryOptions, target string) ([]PaneInfo, error) {
	rows, e := s.listRaw(op, PaneKind, id, opts, target)
	if e != nil {
		return nil, e
	}
	out := make([]PaneInfo, 0, len(rows))
	for _, m := range rows {
		v, e := s.decodePane(m, &id)
		if e != nil {
			return nil, afterError("Panes", e)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return numericID(string(out[i].ID)) < numericID(string(out[j].ID)) })
	return out, nil
}
func (s *Server) Clients(ctx context.Context) ([]ClientInfo, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return nil, opError("Clients", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return nil, opError("Clients", e)
	}
	out, e := s.clients(op, info.Identity)
	return out, opError("Clients", e)
}
func (s *Server) clients(op *operation, id ServerIdentity) ([]ClientInfo, error) {
	rows, e := s.listRaw(op, ClientKind, id, QueryOptions{}, "")
	if e != nil {
		return nil, e
	}
	out := make([]ClientInfo, 0, len(rows))
	for _, m := range rows {
		v, e := s.decodeClient(m, &id)
		if e != nil {
			return nil, afterError("Clients", e)
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
	op, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return nil, e
	}
	return s.inspect(op, kind, id, &guard{identity: info.Identity})
}
func (s *Server) inspect(op *operation, kind ObjectKind, target string, g *guard) (map[string]string, error) {
	fields := fieldsFor(kind)
	args := []string{"-p"}
	if kind == ClientKind {
		args = append(args, "-c", target)
	} else {
		args = append(args, "-t", target)
	}
	args = append(args, codec.RecordFormat(fields))
	r, e := s.execute(op, recordsPlan(command("display-message", args...)), g, nil)
	if e != nil {
		return nil, e
	}
	rows, e := parseRaw(r.Stdout, fields, string(kind))
	if e != nil {
		return nil, afterError("Info", e)
	}
	if len(rows) != 1 {
		return nil, afterError("Info", decodeError(string(kind), "record count", codec.ErrRecord))
	}
	return rows[0], nil
}
func (s *Server) Session(ctx context.Context, id SessionID) (Session, error) {
	m, e := s.lookup(ctx, SessionKind, string(id))
	if e != nil {
		return Session{}, opError("Session", e)
	}
	v, e := s.decodeSession(m, nil)
	if e != nil {
		return Session{}, afterError("Session", e)
	}
	if v.ID != id {
		return Session{}, afterError("Session", decodeError("session", "ID", ErrProtocol))
	}
	return v.Handle(), nil
}
func (s *Server) Window(ctx context.Context, id WindowID) (Window, error) {
	m, e := s.lookup(ctx, WindowKind, string(id))
	if e != nil {
		return Window{}, opError("Window", e)
	}
	v, _, e := s.decodeWindow(m, nil)
	if e != nil {
		return Window{}, afterError("Window", e)
	}
	if v.ID != id {
		return Window{}, afterError("Window", decodeError("window", "ID", ErrProtocol))
	}
	return v.Handle(), nil
}
func (s *Server) Pane(ctx context.Context, id PaneID) (Pane, error) {
	m, e := s.lookup(ctx, PaneKind, string(id))
	if e != nil {
		return Pane{}, opError("Pane", e)
	}
	v, e := s.decodePane(m, nil)
	if e != nil {
		return Pane{}, afterError("Pane", e)
	}
	if v.ID != id {
		return Pane{}, afterError("Pane", decodeError("pane", "ID", ErrProtocol))
	}
	return v.Handle(), nil
}
func (s *Server) FindSession(ctx context.Context, exactName string) (Session, error) {
	if exactName == "" || !codec.ValidString(exactName) {
		return Session{}, opError("FindSession", invalid("name"))
	}
	sessions, e := s.Sessions(ctx)
	if e != nil {
		return Session{}, opError("FindSession", e)
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
func (s *Server) Client(ctx context.Context, name ClientName) (Client, error) {
	if !name.Valid() {
		return Client{}, opError("Client", invalid("client name"))
	}
	clients, e := s.Clients(ctx)
	if e != nil {
		return Client{}, opError("Client", e)
	}
	for _, v := range clients {
		if v.Name == name {
			return v.Handle(), nil
		}
	}
	return Client{}, opError("Client", ErrNotFound)
}
func (h handle) inspect(ctx context.Context) (map[string]string, error) {
	if e := h.check(); e != nil {
		return nil, e
	}
	op, e := h.server.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer op.close()
	return h.server.inspect(op, h.kind, h.id, h.guard())
}
func (s Session) Info(ctx context.Context) (SessionInfo, error) {
	m, e := s.h.inspect(ctx)
	if e != nil {
		return SessionInfo{}, opError("Session.Info", e)
	}
	v, e := s.h.server.decodeSession(m, &s.h.origin)
	if e != nil {
		return SessionInfo{}, afterError("Session.Info", e)
	}
	if v.ID != s.ID() {
		return SessionInfo{}, afterError("Session.Info", ErrProtocol)
	}
	return v, nil
}
func (w Window) Info(ctx context.Context) (WindowInfo, error) {
	m, e := w.h.inspect(ctx)
	if e != nil {
		return WindowInfo{}, opError("Window.Info", e)
	}
	v, _, e := w.h.server.decodeWindow(m, &w.h.origin)
	if e != nil {
		return WindowInfo{}, afterError("Window.Info", e)
	}
	if v.ID != w.ID() {
		return WindowInfo{}, afterError("Window.Info", ErrProtocol)
	}
	return v, nil
}
func (p Pane) Info(ctx context.Context) (PaneInfo, error) {
	m, e := p.h.inspect(ctx)
	if e != nil {
		return PaneInfo{}, opError("Pane.Info", e)
	}
	v, e := p.h.server.decodePane(m, &p.h.origin)
	if e != nil {
		return PaneInfo{}, afterError("Pane.Info", e)
	}
	if v.ID != p.ID() {
		return PaneInfo{}, afterError("Pane.Info", ErrProtocol)
	}
	return v, nil
}
func (c Client) Info(ctx context.Context) (ClientInfo, error) {
	m, e := c.h.inspect(ctx)
	if e != nil {
		return ClientInfo{}, opError("Client.Info", e)
	}
	v, e := c.h.server.decodeClient(m, &c.h.origin)
	if e != nil {
		return ClientInfo{}, afterError("Client.Info", e)
	}
	if v.Name != c.Name() {
		return ClientInfo{}, afterError("Client.Info", ErrProtocol)
	}
	return v, nil
}
func (l WindowLink) Info(ctx context.Context) (WindowLinkInfo, error) {
	if e := l.check(); e != nil {
		return WindowLinkInfo{}, opError("WindowLink.Info", e)
	}
	op, e := l.h.server.begin(ctx)
	if e != nil {
		return WindowLinkInfo{}, opError("WindowLink.Info", e)
	}
	defer op.close()
	m, e := l.h.server.inspect(op, WindowKind, l.target(), l.guard())
	if e != nil {
		return WindowLinkInfo{}, opError("WindowLink.Info", e)
	}
	_, v, e := l.h.server.decodeWindow(m, &l.h.origin)
	if e != nil {
		return WindowLinkInfo{}, afterError("WindowLink.Info", e)
	}
	if v.SessionID != l.session || v.Index != l.index || v.WindowID != WindowID(l.h.id) {
		return WindowLinkInfo{}, afterError("WindowLink.Info", ErrLinkChanged)
	}
	return v, nil
}
func (p Pane) Window(ctx context.Context) (Window, error) {
	v, e := p.Info(ctx)
	if e != nil {
		return Window{}, opError("Pane.Window", e)
	}
	return Window{h: p.h.withOrigin(string(v.WindowID), WindowKind)}, nil
}
func (s Session) Windows(ctx context.Context) ([]WindowLink, error) {
	if e := s.h.check(); e != nil {
		return nil, opError("Session.Windows", e)
	}
	op, e := s.h.server.begin(ctx)
	if e != nil {
		return nil, opError("Session.Windows", e)
	}
	defer op.close()
	_, links, e := s.h.server.windows(op, s.h.origin, QueryOptions{}, s.h.id)
	if e != nil {
		return nil, opError("Session.Windows", e)
	}
	out := make([]WindowLink, 0, len(links))
	for _, l := range links {
		out = append(out, l.Handle())
	}
	return out, nil
}
func (w Window) Links(ctx context.Context) ([]WindowLink, error) {
	if e := w.h.check(); e != nil {
		return nil, opError("Window.Links", e)
	}
	op, e := w.h.server.begin(ctx)
	if e != nil {
		return nil, opError("Window.Links", e)
	}
	defer op.close()
	_, links, e := w.h.server.windows(op, w.h.origin, QueryOptions{}, "")
	if e != nil {
		return nil, opError("Window.Links", e)
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
func (w Window) Panes(ctx context.Context) ([]PaneInfo, error) {
	if e := w.h.check(); e != nil {
		return nil, opError("Window.Panes", e)
	}
	op, e := w.h.server.begin(ctx)
	if e != nil {
		return nil, opError("Window.Panes", e)
	}
	defer op.close()
	p, e := w.h.server.panes(op, w.h.origin, QueryOptions{}, w.h.id)
	return p, opError("Window.Panes", e)
}
func (w Window) ActivePane(ctx context.Context) (Pane, error) {
	panes, e := w.Panes(ctx)
	if e != nil {
		return Pane{}, opError("Window.ActivePane", e)
	}
	for _, p := range panes {
		if p.Active {
			return p.Handle(), nil
		}
	}
	return Pane{}, opError("Window.ActivePane", ErrNotFound)
}
func (p Pane) Format(ctx context.Context, expr Format) ([]byte, error) {
	if e := p.h.check(); e != nil {
		return nil, opError("Pane.Format", e)
	}
	if !codec.ValidString(string(expr)) {
		return nil, opError("Pane.Format", invalid("format"))
	}
	op, e := p.h.server.begin(ctx)
	if e != nil {
		return nil, opError("Pane.Format", e)
	}
	defer op.close()
	r, e := p.h.server.execute(op, recordsPlan(command("display-message", "-p", "-t", p.h.id, codec.ExpressionFormat(string(expr)))), p.h.guard(), nil)
	if e != nil {
		return r.Stdout, opError("Pane.Format", e)
	}
	records, e := codec.ParseRecords(r.Stdout, 1)
	if e != nil || len(records) != 1 {
		return nil, afterError("Pane.Format", errors.Join(e, codec.ErrRecord))
	}
	return []byte(records[0][0]), nil
}
