package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TmuxVars holds the $TMUX and $TMUX_PANE environment variables tmux sets for
// the processes it runs, which locate the caller's server, session, and pane.
type TmuxVars struct {
	// TMUX is the value of $TMUX (formatted as "socket,pid,session").
	TMUX string

	// TMUXPane is the value of $TMUX_PANE (e.g. "%0").
	TMUXPane string
}

// TmuxVarsInfo holds the structured fields parsed from [TmuxVars].
type TmuxVarsInfo struct {
	// SocketPath is the absolute path to the tmux server socket.
	SocketPath string

	// PID is the server process ID reported in the $TMUX variable.
	PID int

	// SessionID is the session ID parsed from the $TMUX variable (prefixed with "$").
	SessionID SessionID

	// PaneID is the pane ID parsed from $TMUX_PANE, or [ValueStateUnavailable] if $TMUX_PANE was empty.
	PaneID Value[PaneID]
}

// CurrentInfo holds verified point-in-time metadata for the pane the caller runs in.
type CurrentInfo struct {
	// Identity is the verified server identity of the answering daemon.
	Identity ServerIdentity

	// Pane is the metadata for the current pane where the caller is running.
	Pane PaneInfo

	// Window is the metadata for the window containing the current pane.
	Window WindowInfo

	// Session is the metadata for the session containing the window, if resolved.
	Session Value[SessionInfo]

	// Link is the metadata for the window link slot within the session, if resolved.
	Link Value[WindowLinkInfo]

	// Client is the metadata for the attached client terminal displaying the current pane, if resolved.
	Client Value[ClientInfo]
}

// ParseTmuxVars parses $TMUX ("socket,pid,session") and $TMUX_PANE.
// Splits trailing comma fields from the end to preserve commas in socket paths.
// Returns [ErrNotInsideTmux] if vars.TMUX is empty. Does not perform I/O.
func ParseTmuxVars(vars TmuxVars) (TmuxVarsInfo, error) {
	if vars.TMUX == "" {
		return TmuxVarsInfo{}, ErrNotInsideTmux
	}

	if strings.ContainsRune(vars.TMUX, 0) {
		return TmuxVarsInfo{}, invalid("TMUX environment")
	}

	end := strings.LastIndexByte(vars.TMUX, ',')
	if end < 0 {
		return TmuxVarsInfo{}, invalid("TMUX trailing session")
	}

	pre := vars.TMUX[:end]
	start := strings.LastIndexByte(pre, ',')

	var (
		socket string
		pid    int
		sid    SessionID
	)

	if start >= 0 {
		sock, p, s, is3Part, err := parseTmux3Part(vars.TMUX, start, end)
		if is3Part {
			if err != nil {
				return TmuxVarsInfo{}, err
			}

			socket = sock
			pid = p
			sid = s
		}
	}

	if socket == "" {
		sock, p, err := parseTmux2Part(vars.TMUX, start, end)
		if err != nil {
			return TmuxVarsInfo{}, err
		}

		socket = sock
		pid = p
	}

	paneID := UnavailableValue[PaneID]()

	if vars.TMUXPane != "" {
		id := PaneID(vars.TMUXPane)
		if !id.Valid() {
			return TmuxVarsInfo{SocketPath: "", PID: 0, SessionID: "", PaneID: UnavailableValue[PaneID]()}, invalid("TMUX_PANE")
		}

		paneID = PresentValue(id)
	}

	return TmuxVarsInfo{SocketPath: socket, PID: pid, SessionID: sid, PaneID: paneID}, nil
}

// Current discovers and verifies the current tmux context (pane, window, session)
// from this process's $TMUX and $TMUX_PANE.
//
// It starts a temporary server handle targeting the socket named in $TMUX, takes a snapshot,
// and returns verified [CurrentInfo]. Returns [ErrNotInsideTmux] if run outside tmux.
func Current(ctx context.Context) (CurrentInfo, error) {
	return CurrentFrom(ctx, TmuxVars{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")})
}

// CurrentFrom is [Current] with the given variables instead of this process's environment.
// Returns [ErrServerChanged] if the environment PID does not match the answering daemon.
func CurrentFrom(ctx context.Context, vars TmuxVars) (CurrentInfo, error) {
	hints, err := ParseTmuxVars(vars)
	if err != nil {
		return CurrentInfo{}, opError("CurrentFrom", err)
	}

	s, err := New(Config{Binary: "", SocketPath: hints.SocketPath, SocketName: "", ConfigFile: "", Env: nil, Dir: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}, UTF8: UTF8Default, Colors256: false, TerminalFeatures: nil, LogLevel: LogLevelNone, LoginShell: false})
	if err != nil {
		return CurrentInfo{}, opError("CurrentFrom", err)
	}

	return s.CurrentFrom(ctx, vars)
}

// CurrentFrom verifies the provided [TmuxVars] against this specific server instance,
// asserting that the socket path and daemon PID match before resolving the pane, window, and session.
// Returns [ErrInvalidHandle] for a socket mismatch and [ErrServerChanged] for a PID mismatch.
func (s *Server) CurrentFrom(ctx context.Context, vars TmuxVars) (CurrentInfo, error) {
	hints, err := ParseTmuxVars(vars)
	if err != nil {
		return CurrentInfo{}, opError("Server.CurrentFrom", err)
	}

	if s == nil || s.endpoint.String() != hints.SocketPath {
		return CurrentInfo{}, opError("Server.CurrentFrom", ErrInvalidHandle)
	}

	snap, err := s.Snapshot(ctx)
	if err != nil {
		return CurrentInfo{}, opError("Server.CurrentFrom", err)
	}

	if hints.PID != snap.Identity.PID {
		return CurrentInfo{}, opError("Server.CurrentFrom", ErrServerChanged)
	}

	paneID, ok := hints.PaneID.Get()
	if !ok {
		info, err := resolveActiveContext(snap, hints)
		if err != nil {
			return CurrentInfo{}, opError("Server.CurrentFrom", err)
		}

		return info, nil
	}

	info, err := resolvePaneContext(snap, paneID)
	if err != nil {
		return CurrentInfo{}, opError("Server.CurrentFrom", err)
	}

	return info, nil
}

func isNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}

func parseTmux3Part(raw string, start, end int) (string, int, SessionID, bool, error) {
	if !isNumeric(raw[start+1 : end]) {
		return "", 0, "", false, nil
	}

	p, _ := strconv.Atoi(raw[start+1 : end])
	s := SessionID("$" + raw[end+1:])

	sock := raw[:start]
	if p <= 0 || !filepath.IsAbs(sock) {
		return "", 0, "", true, invalid("TMUX socket or PID")
	}

	if !s.Valid() {
		return "", 0, "", true, invalid("TMUX session ID")
	}

	return sock, p, s, true, nil
}

func parseTmux2Part(raw string, start, end int) (string, int, error) {
	pStr := raw[end+1:]
	p, err := strconv.Atoi(pStr)

	sock := raw[:end]
	if err != nil || p <= 0 || !filepath.IsAbs(sock) || strconv.Itoa(p) != pStr {
		if start < 0 {
			return "", 0, invalid("TMUX trailing PID")
		}

		return "", 0, invalid("TMUX socket or PID")
	}

	return sock, p, nil
}

func resolvePaneContext(snap Snapshot, paneID PaneID) (CurrentInfo, error) {
	p, ok := snap.Pane(paneID)
	if !ok {
		return CurrentInfo{}, ErrNotFound
	}

	w, ok := snap.Window(p.WindowID)
	if !ok {
		return CurrentInfo{}, ErrInconsistent
	}

	out := CurrentInfo{
		Identity: snap.Identity,
		Pane:     p,
		Window:   w,
		Session:  UnavailableValue[SessionInfo](),
		Link:     UnavailableValue[WindowLinkInfo](),
		Client:   UnavailableValue[ClientInfo](),
	}

	var links []WindowLinkInfo
	for _, l := range snap.links {
		if l.WindowID == w.ID {
			links = append(links, l)
		}
	}

	if len(links) == 1 {
		out.Link = PresentValue(links[0])

		session, ok := snap.Session(links[0].SessionID)
		if !ok {
			return CurrentInfo{}, ErrInconsistent
		}

		out.Session = PresentValue(session)
	}

	return out, nil
}

func resolveActiveContext(snap Snapshot, hints TmuxVarsInfo) (CurrentInfo, error) {
	var (
		targetSession SessionInfo
		foundSession  bool
	)

	if hints.SessionID.Valid() {
		targetSession, foundSession = snap.Session(hints.SessionID)
	} else if len(snap.sessions) == 1 {
		targetSession = snap.sessions[0]
		foundSession = true
	}

	if !foundSession {
		return CurrentInfo{}, invalid("TMUX_PANE required for verified pane context")
	}

	activeLink, ok := lookupActiveLink(snap.links, targetSession.ID)
	if !ok {
		return CurrentInfo{}, invalid("TMUX_PANE required for verified pane context")
	}

	w, ok := snap.Window(activeLink.WindowID)
	if !ok {
		return CurrentInfo{}, invalid("TMUX_PANE required for verified pane context")
	}

	activePane, ok := lookupActivePane(snap.panes, w.ID)
	if !ok {
		return CurrentInfo{}, invalid("TMUX_PANE required for verified pane context")
	}

	return CurrentInfo{
		Identity: snap.Identity,
		Pane:     activePane,
		Window:   w,
		Session:  PresentValue(targetSession),
		Link:     PresentValue(activeLink),
		Client:   UnavailableValue[ClientInfo](),
	}, nil
}

func lookupActiveLink(links []WindowLinkInfo, sessionID SessionID) (WindowLinkInfo, bool) {
	for _, l := range links {
		if l.SessionID == sessionID && l.Active {
			return l, true
		}
	}

	var zero WindowLinkInfo

	return zero, false
}

func lookupActivePane(panes []PaneInfo, windowID WindowID) (PaneInfo, bool) {
	for _, p := range panes {
		if p.WindowID == windowID && p.Active {
			return p, true
		}
	}

	var zero PaneInfo

	return zero, false
}
