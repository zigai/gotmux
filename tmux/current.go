package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment holds raw tmux environment variable values passed to child processes.
type Environment struct {
	// TMUX is the value of the ambient $TMUX environment variable (formatted as "socket,pid,session").
	TMUX string

	// TMUXPane is the value of the ambient $TMUX_PANE environment variable (e.g. "%0").
	TMUXPane string
}

// EnvironmentInfo holds the structured fields parsed from [Environment].
type EnvironmentInfo struct {
	// SocketPath is the absolute path to the tmux server socket.
	SocketPath string

	// PID is the server process ID reported in the $TMUX variable.
	PID int

	// SessionID is the session ID parsed from the $TMUX variable (prefixed with "$").
	SessionID SessionID

	// PaneID is the pane ID parsed from $TMUX_PANE, or Unavailable if $TMUX_PANE was empty.
	PaneID Value[PaneID]
}

// CurrentInfo holds verified point-in-time metadata for the caller's ambient tmux context.
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

// ParseEnvironment parses $TMUX ("socket,pid,session") and $TMUX_PANE.
// Splits trailing comma fields from the end to preserve commas in socket paths.
// Returns [ErrNotInsideTmux] if env.TMUX is empty. Does not perform I/O.
func ParseEnvironment(env Environment) (EnvironmentInfo, error) {
	if env.TMUX == "" {
		return EnvironmentInfo{}, ErrNotInsideTmux
	}

	if strings.ContainsRune(env.TMUX, 0) {
		return EnvironmentInfo{}, invalid("TMUX environment")
	}

	end := strings.LastIndexByte(env.TMUX, ',')
	if end < 0 {
		return EnvironmentInfo{}, invalid("TMUX trailing session")
	}

	pre := env.TMUX[:end]
	start := strings.LastIndexByte(pre, ',')

	var (
		socket string
		pid    int
		sid    SessionID
	)

	if start >= 0 {
		sock, p, s, is3Part, err := parseTmux3Part(env.TMUX, start, end)
		if is3Part {
			if err != nil {
				return EnvironmentInfo{}, err
			}

			socket = sock
			pid = p
			sid = s
		}
	}

	if socket == "" {
		sock, p, err := parseTmux2Part(env.TMUX, start, end)
		if err != nil {
			return EnvironmentInfo{}, err
		}

		socket = sock
		pid = p
	}

	paneID := UnavailableValue[PaneID]()

	if env.TMUXPane != "" {
		id := PaneID(env.TMUXPane)
		if !id.Valid() {
			return EnvironmentInfo{SocketPath: "", PID: 0, SessionID: "", PaneID: UnavailableValue[PaneID]()}, invalid("TMUX_PANE")
		}

		paneID = PresentValue(id)
	}

	return EnvironmentInfo{SocketPath: socket, PID: pid, SessionID: sid, PaneID: paneID}, nil
}

// Current discovers and verifies the current tmux context (pane, window, session)
// from ambient environment variables ($TMUX and $TMUX_PANE).
//
// It starts a temporary server handle targeting the ambient socket, takes a snapshot,
// and returns verified [CurrentInfo]. Returns [ErrNotInsideTmux] if run outside tmux.
func Current(ctx context.Context) (CurrentInfo, error) {
	return CurrentWithEnv(ctx, Environment{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")})
}

// CurrentWithEnv discovers and verifies current tmux context using the provided [Environment] values.
func CurrentWithEnv(ctx context.Context, env Environment) (CurrentInfo, error) {
	hints, err := ParseEnvironment(env)
	if err != nil {
		return CurrentInfo{}, opError("Current", err)
	}

	s, err := New(Config{Binary: "", SocketPath: hints.SocketPath, SocketName: "", ConfigFile: "", Env: nil, Dir: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if err != nil {
		return CurrentInfo{}, opError("Current", err)
	}

	return s.CurrentWithEnv(ctx, env)
}

// CurrentWithEnv verifies the provided [Environment] against this specific server instance,
// asserting that the socket path matches and resolving the active pane, window, and session.
func (s *Server) CurrentWithEnv(ctx context.Context, env Environment) (CurrentInfo, error) {
	hints, err := ParseEnvironment(env)
	if err != nil {
		return CurrentInfo{}, opError("Current", err)
	}

	if s == nil || s.endpoint.String() != hints.SocketPath {
		return CurrentInfo{}, opError("Current", ErrInvalidHandle)
	}

	snap, err := s.Snapshot(ctx)
	if err != nil {
		return CurrentInfo{}, opError("Current", err)
	}

	pid, ok := hints.PaneID.Get()
	if !ok {
		info, err := resolveActiveContext(snap, hints)
		if err != nil {
			return CurrentInfo{}, opError("Current", err)
		}

		return info, nil
	}

	info, err := resolvePaneContext(snap, pid)
	if err != nil {
		return CurrentInfo{}, opError("Current", err)
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

func resolvePaneContext(snap Snapshot, pid PaneID) (CurrentInfo, error) {
	p, ok := snap.Pane(pid)
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

func resolveActiveContext(snap Snapshot, hints EnvironmentInfo) (CurrentInfo, error) {
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

	activeLink, ok := findActiveLink(snap.links, targetSession.ID)
	if !ok {
		return CurrentInfo{}, invalid("TMUX_PANE required for verified pane context")
	}

	w, ok := snap.Window(activeLink.WindowID)
	if !ok {
		return CurrentInfo{}, invalid("TMUX_PANE required for verified pane context")
	}

	activePane, ok := findActivePane(snap.panes, w.ID)
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

func findActiveLink(links []WindowLinkInfo, sessionID SessionID) (WindowLinkInfo, bool) {
	for _, l := range links {
		if l.SessionID == sessionID && l.Active {
			return l, true
		}
	}

	var zero WindowLinkInfo

	return zero, false
}

func findActivePane(panes []PaneInfo, windowID WindowID) (PaneInfo, bool) {
	for _, p := range panes {
		if p.WindowID == windowID && p.Active {
			return p, true
		}
	}

	var zero PaneInfo

	return zero, false
}
