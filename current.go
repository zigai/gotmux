package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Environment struct {
	TMUX     string
	TMUXPane string
}
type EnvironmentInfo struct {
	SocketPath string
	PID        int
	SessionID  SessionID
	PaneID     Value[PaneID]
}

// ParseEnvironment is pure; it does not verify hints. The socket may contain
// commas, so only the two documented trailing fields are split.
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
	pid, e := strconv.Atoi(pre[start+1:])
	if e != nil || pid <= 0 || !filepath.IsAbs(socket) {
		return EnvironmentInfo{}, invalid("TMUX socket or PID")
	}
	sid := SessionID("$" + env.TMUX[end+1:])
	if !sid.Valid() {
		return EnvironmentInfo{}, invalid("TMUX session ID")
	}
	out := EnvironmentInfo{SocketPath: socket, PID: pid, SessionID: sid}
	if env.TMUXPane != "" {
		id := PaneID(env.TMUXPane)
		if !id.Valid() {
			return EnvironmentInfo{}, invalid("TMUX_PANE")
		}
		out.PaneID = PresentValue(id)
	}
	return out, nil
}

type CurrentInfo struct {
	Identity ServerIdentity
	Pane     PaneInfo
	Window   WindowInfo
	Session  Value[SessionInfo]
	Link     Value[WindowLinkInfo]
	Client   Value[ClientInfo]
}

func Current(ctx context.Context) (CurrentInfo, error) {
	return CurrentWithEnv(ctx, Environment{TMUX: os.Getenv("TMUX"), TMUXPane: os.Getenv("TMUX_PANE")})
}
func CurrentWithEnv(ctx context.Context, env Environment) (CurrentInfo, error) {
	hints, e := ParseEnvironment(env)
	if e != nil {
		return CurrentInfo{}, opError("Current", e)
	}
	s, e := New(Config{SocketPath: hints.SocketPath})
	if e != nil {
		return CurrentInfo{}, opError("Current", e)
	}
	return s.CurrentWithEnv(ctx, env)
}
func (s *Server) CurrentWithEnv(ctx context.Context, env Environment) (CurrentInfo, error) {
	hints, e := ParseEnvironment(env)
	if e != nil {
		return CurrentInfo{}, opError("Current", e)
	}
	if s == nil || s.endpoint.String() != hints.SocketPath {
		return CurrentInfo{}, opError("Current", ErrInvalidHandle)
	}
	pid, ok := hints.PaneID.Get()
	if !ok {
		return CurrentInfo{}, opError("Current", invalid("TMUX_PANE required for verified pane context"))
	}
	snap, e := s.Snapshot(ctx)
	if e != nil {
		return CurrentInfo{}, opError("Current", e)
	}
	p, ok := snap.Pane(pid)
	if !ok {
		return CurrentInfo{}, opError("Current", ErrNotFound)
	}
	w, ok := snap.Window(p.WindowID)
	if !ok {
		return CurrentInfo{}, opError("Current", ErrInconsistent)
	}
	out := CurrentInfo{Identity: snap.Identity, Pane: p, Window: w}
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
