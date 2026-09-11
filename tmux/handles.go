package tmux

import (
	"context"
	"strconv"
	"time"
)

var zeroHandle handle

type (
	// SessionID is a canonical tmux session identifier prefixed with "$" (e.g. "$0").
	SessionID string

	// WindowID is a canonical tmux window identifier prefixed with "@" (e.g. "@0").
	WindowID string

	// PaneID is a canonical tmux pane identifier prefixed with "%" (e.g. "%0").
	PaneID string

	// ClientName identifies an attached client terminal (e.g. "/dev/pts/1").
	ClientName string

	handle struct {
		server *Server
		origin ServerIdentity
		id     string
		kind   ObjectKind
		client clientCheck
	}

	// Session is an opaque handle to a tmux session bound to a verified daemon identity.
	Session struct{ h handle }

	// Window is an opaque handle to a shared tmux window object.
	// Killing a Window terminates it across all sessions where it is linked.
	Window struct{ h handle }

	// Pane is an opaque handle to a tmux pane inside a window.
	Pane struct{ h handle }

	// Client is an opaque handle to an attached client terminal.
	Client struct{ h handle }

	// WindowLink represents a window linked into a specific session at a specific slot index.
	// In tmux, a single Window may be linked into multiple sessions simultaneously at different
	// indices. Unlinking a WindowLink only removes the window from that session slot; it does
	// not kill the window unless it was the last link.
	WindowLink struct {
		h       handle
		session SessionID
		index   int
	}
)

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

	_, err := strconv.ParseUint(s[1:], 10, 32)

	return err == nil
}

// Valid reports whether this string has a valid tmux session ID syntax:
// a "$" prefix followed by a non-negative decimal integer with no leading zeroes (e.g. "$0", "$1").
func (i SessionID) Valid() bool { return validID(string(i), '$') }

// Valid reports whether this string has a valid tmux window ID syntax:
// an "@" prefix followed by a non-negative decimal integer (e.g. "@0", "@1").
func (i WindowID) Valid() bool { return validID(string(i), '@') }

// Valid reports whether this string has a valid tmux pane ID syntax:
// a "%" prefix followed by a non-negative decimal integer (e.g. "%0", "%1").
func (i PaneID) Valid() bool { return validID(string(i), '%') }

// Valid reports whether the client name is non-empty and contains no NUL or line break bytes.
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

func (h handle) valid() bool {
	if h.server == nil || (!h.origin.isZero() && !h.origin.valid()) {
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
	case LinkKind:
		return false
	default:
		return false
	}
}

func (s *Server) unprobedHandle(id string, kind ObjectKind) handle {
	return handle{
		server: s,
		origin: ServerIdentity{
			Endpoint:       Endpoint{SocketPath: "", SocketName: "", TempDir: "", UID: 0},
			ReportedSocket: "",
			PID:            0,
			Started:        time.Time{},
			Generation:     0,
		},
		id:   id,
		kind: kind,
		client: clientCheck{
			name:    "",
			pid:     0,
			created: 0,
		},
	}
}

func (h handle) check() error {
	if !h.valid() {
		return ErrInvalidHandle
	}

	if h.origin.valid() {
		if h.server.endpoint != h.origin.Endpoint {
			return ErrInvalidHandle
		}

		if h.server.conn != nil && h.server.conn.generation != h.origin.Generation {
			return ErrInvalidHandle
		}
	}

	if h.server.lifetime != nil {
		if err := h.server.lifetime.closedError(); err != nil {
			return err
		}
	}

	return nil
}

func (h handle) guard() *guard {
	if !h.origin.valid() {
		return nil
	}

	g := newGuard(h.origin)
	if h.kind == ClientKind {
		g.clients = []clientCheck{h.client}
	}

	return g
}

func (h handle) equal(other handle) bool {
	return h.valid() && other.valid() && h.kind == other.kind && h.id == other.id && h.origin.Equal(other.origin) && h.client == other.client
}

func (h handle) act(ctx context.Context, name string, args ...string) error {
	if err := h.check(); err != nil {
		return opError(name, err)
	}

	opCtx, op, err := h.server.begin(ctx)
	if err != nil {
		return opError(name, err)
	}
	defer op.close()

	_, err = h.server.execute(opCtx, op, emptyPlan(command(name, args...)), h.guard(), nil)

	return opError(name, err)
}

func (h handle) withOrigin(id string, kind ObjectKind) handle {
	return handle{server: h.server, origin: h.origin, id: id, kind: kind, client: clientCheck{name: "", pid: 0, created: 0}}
}

// ID returns the canonical tmux session ID string (e.g. "$0").
func (s Session) ID() SessionID { return SessionID(s.h.id) }

// Valid reports whether this handle has valid syntax and known server provenance.
// It does NOT perform I/O to check if the session is still alive on the daemon.
func (s Session) Valid() bool { return s.h.valid() }

// Equal reports whether two handles refer to the same session on the exact same daemon lifetime.
func (s Session) Equal(other Session) bool { return s.h.equal(other.h) }

// Identity returns the server identity where this session handle was created or observed.
func (s Session) Identity() ServerIdentity { return s.h.origin }

// ID returns the canonical tmux window ID string (e.g. "@1").
func (w Window) ID() WindowID { return WindowID(w.h.id) }

// Valid reports whether this handle has valid syntax and known server provenance.
// It does NOT perform I/O to check if the window is still alive.
func (w Window) Valid() bool { return w.h.valid() }

// Equal reports whether two handles refer to the same window on the exact same daemon lifetime.
func (w Window) Equal(other Window) bool { return w.h.equal(other.h) }

// Identity returns the server identity where this window handle was created or observed.
func (w Window) Identity() ServerIdentity { return w.h.origin }

// ID returns the canonical tmux pane ID string (e.g. "%2").
func (p Pane) ID() PaneID { return PaneID(p.h.id) }

// Valid reports whether this handle has valid syntax and known server provenance.
// It does NOT perform I/O to check if the pane is still alive.
func (p Pane) Valid() bool { return p.h.valid() }

// Equal reports whether two handles refer to the same pane on the exact same daemon lifetime.
func (p Pane) Equal(other Pane) bool { return p.h.equal(other.h) }

// Identity returns the server identity where this pane handle was created or observed.
func (p Pane) Identity() ServerIdentity { return p.h.origin }

// Name returns the client terminal name (e.g. "/dev/pts/1").
func (c Client) Name() ClientName { return ClientName(c.h.id) }

// Valid reports whether this handle has valid client syntax and known server provenance.
func (c Client) Valid() bool { return c.h.valid() }

// Equal reports whether two handles refer to the same client on the exact same daemon lifetime.
func (c Client) Equal(other Client) bool { return c.h.equal(other.h) }

// Identity returns the server identity where this client handle was created or observed.
func (c Client) Identity() ServerIdentity { return c.h.origin }

// Valid reports whether this link has valid window handle provenance, a valid session ID,
// and a non-negative slot index. It does NOT perform I/O to query daemon state.
func (l WindowLink) Valid() bool {
	return l.h.valid() && l.h.kind == WindowKind && l.session.Valid() && l.index >= 0
}

// Equal reports whether two window links refer to the same (session, index, window)
// slot on the exact same daemon lifetime.
func (l WindowLink) Equal(other WindowLink) bool {
	return l.Valid() && other.Valid() && l.h.equal(other.h) && l.session == other.session && l.index == other.index
}

// Index returns the 0-based slot index where this window is linked in the session.
func (l WindowLink) Index() int { return l.index }

// Identity returns the server identity where this window link was created or observed.
func (l WindowLink) Identity() ServerIdentity { return l.h.origin }

// Window returns the underlying [Window] handle for this link.
func (l WindowLink) Window() Window {
	if !l.Valid() {
		return Window{h: zeroHandle}
	}

	return Window{h: l.h}
}

// Session returns the parent [Session] handle containing this window link.
func (l WindowLink) Session() Session {
	if !l.Valid() {
		return Session{h: zeroHandle}
	}

	return Session{h: l.h.withOrigin(string(l.session), SessionKind)}
}

// UsingSubprocess preserves the observed slot and connection lifetime.
func (l WindowLink) UsingSubprocess() (WindowLink, error) {
	if err := l.check(); err != nil {
		return WindowLink{}, opError("UsingSubprocess", err)
	}

	h, err := l.h.usingSubprocess()
	if err != nil {
		return WindowLink{}, err
	}

	l.h = h

	return l, nil
}

func (l WindowLink) target() string { return string(l.session) + ":" + strconv.Itoa(l.index) }
func (l WindowLink) check() error {
	if !l.Valid() {
		return ErrInvalidHandle
	}

	return l.h.check()
}

func (l WindowLink) guard() *guard {
	return &guard{identity: l.h.origin, links: []linkCheck{{session: l.session, index: l.index, window: WindowID(l.h.id)}}, clients: nil}
}

func (l WindowLink) act(ctx context.Context, name string, args ...string) error {
	if err := l.check(); err != nil {
		return opError(name, err)
	}

	opCtx, op, err := l.h.server.begin(ctx)
	if err != nil {
		return opError(name, err)
	}
	defer op.close()

	_, err = l.h.server.execute(opCtx, op, emptyPlan(command(name, args...)), l.guard(), nil)

	return opError(name, err)
}

func sameHandles(a, b handle) error {
	if err := a.check(); err != nil {
		return err
	}

	if err := b.check(); err != nil {
		return err
	}

	if !a.origin.Equal(b.origin) {
		return ErrInvalidHandle
	}

	return nil
}

func (h handle) usingSubprocess() (handle, error) {
	if err := h.check(); err != nil {
		return handle{}, opError("UsingSubprocess", err)
	}

	server, err := h.server.UsingSubprocess()
	if err != nil {
		return handle{}, err
	}

	h.server = server

	return h, nil
}

// UsingSubprocess preserves this handle's daemon identity and connection lifetime.
func (p Pane) UsingSubprocess() (Pane, error) {
	h, err := p.h.usingSubprocess()
	return Pane{h: h}, err
}

// UsingSubprocess preserves this handle's daemon identity and connection lifetime.
func (s Session) UsingSubprocess() (Session, error) {
	h, err := s.h.usingSubprocess()
	return Session{h: h}, err
}

// UsingSubprocess preserves this handle's daemon identity and connection lifetime.
func (w Window) UsingSubprocess() (Window, error) {
	h, err := w.h.usingSubprocess()
	return Window{h: h}, err
}

// UsingSubprocess preserves this handle's daemon identity and connection lifetime.
func (c Client) UsingSubprocess() (Client, error) {
	h, err := c.h.usingSubprocess()
	return Client{h: h}, err
}
