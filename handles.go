package tmux

import (
	"context"
	"strconv"
)

type SessionID string
type WindowID string
type PaneID string
type ClientName string

func validID(s string, prefix byte) bool {
	if len(s) < 2 || s[0] != prefix {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	if len(s) > 2 && s[1] == '0' {
		return false
	}
	_, e := strconv.ParseUint(s[1:], 10, 32)
	return e == nil
}
func (i SessionID) Valid() bool { return validID(string(i), '$') }
func (i WindowID) Valid() bool  { return validID(string(i), '@') }
func (i PaneID) Valid() bool    { return validID(string(i), '%') }
func (n ClientName) Valid() bool {
	if n == "" {
		return false
	}
	for _, c := range []byte(n) {
		if c == 0 || c == '\n' || c == '\r' {
			return false
		}
	}
	return true
}

type handle struct {
	server *Server
	origin ServerIdentity
	id     string
	kind   ObjectKind
	client clientCheck
}

func (h handle) valid() bool {
	if h.server == nil || !h.origin.valid() {
		return false
	}
	switch h.kind {
	case SessionKind:
		return SessionID(h.id).Valid()
	case WindowKind:
		return WindowID(h.id).Valid()
	case PaneKind:
		return PaneID(h.id).Valid()
	case ClientKind:
		return ClientName(h.id).Valid()
	}
	return false
}
func (h handle) check() error {
	if !h.valid() {
		return ErrInvalidHandle
	}
	if h.server.endpoint != h.origin.Endpoint {
		return ErrInvalidHandle
	}
	if h.server.lifetime != nil {
		if e := h.server.lifetime.closedError(); e != nil {
			return e
		}
	}
	if h.server.conn != nil && h.server.conn.generation != h.origin.Generation {
		return ErrInvalidHandle
	}
	return nil
}
func (h handle) guard() *guard {
	g := &guard{identity: h.origin}
	if h.kind == ClientKind {
		g.clients = []clientCheck{h.client}
	}
	return g
}
func (h handle) equal(other handle) bool {
	return h.valid() && other.valid() && h.kind == other.kind && h.id == other.id && h.origin.Equal(other.origin) && h.client == other.client
}
func (h handle) act(ctx context.Context, name string, args ...string) error {
	if e := h.check(); e != nil {
		return opError(name, e)
	}
	op, e := h.server.begin(ctx)
	if e != nil {
		return opError(name, e)
	}
	defer op.close()
	_, e = h.server.execute(op, emptyPlan(command(name, args...)), h.guard(), nil)
	return opError(name, e)
}
func (h handle) withOrigin(id string, kind ObjectKind) handle {
	return handle{server: h.server, origin: h.origin, id: id, kind: kind}
}

type Session struct{ h handle }
type Window struct{ h handle }
type Pane struct{ h handle }
type Client struct{ h handle }

func (s Session) ID() SessionID            { return SessionID(s.h.id) }
func (s Session) Valid() bool              { return s.h.valid() }
func (s Session) Equal(other Session) bool { return s.h.equal(other.h) }
func (s Session) Identity() ServerIdentity { return s.h.origin }
func (w Window) ID() WindowID              { return WindowID(w.h.id) }
func (w Window) Valid() bool               { return w.h.valid() }
func (w Window) Equal(other Window) bool   { return w.h.equal(other.h) }
func (w Window) Identity() ServerIdentity  { return w.h.origin }
func (p Pane) ID() PaneID                  { return PaneID(p.h.id) }
func (p Pane) Valid() bool                 { return p.h.valid() }
func (p Pane) Equal(other Pane) bool       { return p.h.equal(other.h) }
func (p Pane) Identity() ServerIdentity    { return p.h.origin }
func (c Client) Name() ClientName          { return ClientName(c.h.id) }
func (c Client) Valid() bool               { return c.h.valid() }
func (c Client) Equal(other Client) bool   { return c.h.equal(other.h) }
func (c Client) Identity() ServerIdentity  { return c.h.origin }

type WindowLink struct {
	h       handle
	session SessionID
	index   int
}

func (l WindowLink) Valid() bool {
	return l.h.valid() && l.h.kind == WindowKind && l.session.Valid() && l.index >= 0
}
func (l WindowLink) Equal(other WindowLink) bool {
	return l.Valid() && other.Valid() && l.h.equal(other.h) && l.session == other.session && l.index == other.index
}
func (l WindowLink) Index() int               { return l.index }
func (l WindowLink) Identity() ServerIdentity { return l.h.origin }
func (l WindowLink) Window() Window {
	if !l.Valid() {
		return Window{}
	}
	return Window{h: l.h}
}
func (l WindowLink) Session() Session {
	if !l.Valid() {
		return Session{}
	}
	return Session{h: l.h.withOrigin(string(l.session), SessionKind)}
}
func (l WindowLink) target() string { return string(l.session) + ":" + strconv.Itoa(l.index) }
func (l WindowLink) check() error {
	if !l.Valid() {
		return ErrInvalidHandle
	}
	return l.h.check()
}
func (l WindowLink) guard() *guard {
	return &guard{identity: l.h.origin, links: []linkCheck{{session: l.session, index: l.index, window: WindowID(l.h.id)}}}
}
func (l WindowLink) act(ctx context.Context, name string, args ...string) error {
	if e := l.check(); e != nil {
		return opError(name, e)
	}
	op, e := l.h.server.begin(ctx)
	if e != nil {
		return opError(name, e)
	}
	defer op.close()
	_, e = l.h.server.execute(op, emptyPlan(command(name, args...)), l.guard(), nil)
	return opError(name, e)
}
func sameHandles(a, b handle) error {
	if e := a.check(); e != nil {
		return e
	}
	if e := b.check(); e != nil {
		return e
	}
	if !a.origin.Equal(b.origin) {
		return ErrInvalidHandle
	}
	return nil
}

// UsingSubprocess explicitly selects an auxiliary process for a control-bound
// pane. Its original daemon and connection lifetime remain pinned. It never
// silently falls back from control after a failure.
func (p Pane) UsingSubprocess() (Pane, error) {
	if e := p.h.check(); e != nil {
		return Pane{}, opError("UsingSubprocess", e)
	}
	if p.h.server.conn == nil {
		return p, nil
	}
	h := p.h
	h.server = p.h.server.conn.AuxiliaryServer()
	return Pane{h: h}, nil
}
