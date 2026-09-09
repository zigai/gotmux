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

// ParseEnvironment parses the $TMUX and $TMUX_PANE environment variable strings into
// structured [EnvironmentInfo].
//
// In tmux, $TMUX is formatted as "<socket-path>,<pid>,<session-index>". Because UNIX socket
// paths may legitimately contain commas, ParseEnvironment splits only the two trailing
// comma-separated fields, preserving commas in the socket path.
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
	if start < 0 {
		return EnvironmentInfo{}, invalid("TMUX trailing PID")
	}

	socket := pre[:start]

	pid, err := strconv.Atoi(pre[start+1:])
	if err != nil || pid <= 0 || !filepath.IsAbs(socket) {
		return EnvironmentInfo{}, invalid("TMUX socket or PID")
	}

	sid := SessionID("$" + env.TMUX[end+1:])
	if !sid.Valid() {
		return EnvironmentInfo{}, invalid("TMUX session ID")
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

	pid, ok := hints.PaneID.Get()
	if !ok {
		return CurrentInfo{}, opError("Current", invalid("TMUX_PANE required for verified pane context"))
	}

	snap, err := s.Snapshot(ctx)
	if err != nil {
		return CurrentInfo{}, opError("Current", err)
	}

	p, ok := snap.Pane(pid)
	if !ok {
		return CurrentInfo{}, opError("Current", ErrNotFound)
	}

	w, ok := snap.Window(p.WindowID)
	if !ok {
		return CurrentInfo{}, opError("Current", ErrInconsistent)
	}

	out := CurrentInfo{Identity: snap.Identity, Pane: p, Window: w, Session: UnavailableValue[SessionInfo](), Link: UnavailableValue[WindowLinkInfo](), Client: UnavailableValue[ClientInfo]()}

	var links []WindowLinkInfo
	for _, l := range snap.links {
		if l.WindowID == w.ID {
			links = append(links, l)
		}
	}

	if len(links) == 1 {
		out.Link = PresentValue(links[0])
		if session, ok := snap.Session(links[0].SessionID); ok {
			out.Session = PresentValue(session)
		} else {
			return CurrentInfo{}, opError("Current", ErrInconsistent)
		}
	}
	// An old TMUX session hint never overrides the pane's live graph. Multiple
	// links or clients are explicitly unavailable rather than arbitrarily chosen.
	return out, nil
}
