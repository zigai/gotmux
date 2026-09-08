package tmux

import (
	"context"
	"time"
)

type Consistency uint8

const (
	Consistent Consistency = iota // No contradiction detected; never an atomicity claim.
	Incomplete
)

type MissingReference struct {
	Kind           ObjectKind
	ID             string
	ReferencedKind ObjectKind
	ReferencedID   string
	Reason         string
}

// Snapshot is a bounded observation. Its local accessors perform no I/O and
// return copies. Collection uses a fixed number of batched queries, not one
// process per pane. Consistent does not mean an atomic transaction.
type Snapshot struct {
	Started      time.Time
	Finished     time.Time
	Identity     ServerIdentity
	Consistency  Consistency
	MissingCount int
	sessions     []SessionInfo
	windows      []WindowInfo
	links        []WindowLinkInfo
	panes        []PaneInfo
	clients      []ClientInfo
	missing      []MissingReference
}

func (v Snapshot) Sessions() []SessionInfo { return append([]SessionInfo{}, v.sessions...) }
func (v Snapshot) Windows() []WindowInfo   { return append([]WindowInfo{}, v.windows...) }
func (v Snapshot) Links() []WindowLinkInfo { return append([]WindowLinkInfo{}, v.links...) }
func (v Snapshot) Panes() []PaneInfo       { return append([]PaneInfo{}, v.panes...) }
func (v Snapshot) Clients() []ClientInfo {
	out := append([]ClientInfo{}, v.clients...)
	for i := range out {
		out[i].Flags = append([]string{}, out[i].Flags...)
	}
	return out
}
func (v Snapshot) MissingReferences() []MissingReference {
	return append([]MissingReference{}, v.missing...)
}
func (v Snapshot) Pane(id PaneID) (PaneInfo, bool) {
	for _, p := range v.panes {
		if p.h.id == string(id) {
			return p, true
		}
	}
	return PaneInfo{}, false
}
func (v Snapshot) Window(id WindowID) (WindowInfo, bool) {
	for _, w := range v.windows {
		if w.h.id == string(id) {
			return w, true
		}
	}
	return WindowInfo{}, false
}
func (v Snapshot) Session(id SessionID) (SessionInfo, bool) {
	for _, s := range v.sessions {
		if s.h.id == string(id) {
			return s, true
		}
	}
	return SessionInfo{}, false
}
func (v Snapshot) Client(name ClientName) (ClientInfo, bool) {
	for _, c := range v.clients {
		if c.h.id == string(name) {
			c.Flags = append([]string{}, c.Flags...)
			return c, true
		}
	}
	return ClientInfo{}, false
}
func (v Snapshot) ResolvePane(id PaneID) (Pane, bool) { p, ok := v.Pane(id); return p.Handle(), ok }
func (v Snapshot) ResolveWindow(id WindowID) (Window, bool) {
	w, ok := v.Window(id)
	return w.Handle(), ok
}
func (v Snapshot) ResolveSession(id SessionID) (Session, bool) {
	s, ok := v.Session(id)
	return s.Handle(), ok
}
func (v Snapshot) ResolveClient(name ClientName) (Client, bool) {
	c, ok := v.Client(name)
	return c.Handle(), ok
}

func (s *Server) Snapshot(ctx context.Context) (Snapshot, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return Snapshot{}, opError("Snapshot", e)
	}
	defer op.close()
	out := Snapshot{Started: time.Now()}
	info, e := s.probe(op)
	if e != nil {
		return Snapshot{}, opError("Snapshot", e)
	}
	out.Identity = info.Identity
	out.sessions, e = s.sessions(op, info.Identity, QueryOptions{})
	if e != nil {
		return Snapshot{}, opError("Snapshot", e)
	}
	out.windows, out.links, e = s.windows(op, info.Identity, QueryOptions{}, "")
	if e != nil {
		return Snapshot{}, opError("Snapshot", e)
	}
	out.panes, e = s.panes(op, info.Identity, QueryOptions{}, "")
	if e != nil {
		return Snapshot{}, opError("Snapshot", e)
	}
	out.clients, e = s.clients(op, info.Identity)
	if e != nil {
		return Snapshot{}, opError("Snapshot", e)
	}
	if e = op.ctx.Err(); e != nil {
		return Snapshot{}, afterError("Snapshot", e)
	}
	out.Finished = time.Now()
	out.assess()
	return out, nil
}
func (v *Snapshot) assess() {
	sessions := map[SessionID]bool{}
	windows := map[WindowID]bool{}
	counts := map[SessionID]int{}
	paneCounts := map[WindowID]int{}
	for _, s := range v.sessions {
		sessions[s.ID] = true
	}
	for _, w := range v.windows {
		windows[w.ID] = true
	}
	add := func(m MissingReference) {
		v.Consistency = Incomplete
		v.MissingCount++
		if len(v.missing) < 128 {
			v.missing = append(v.missing, m)
		}
	}
	for _, l := range v.links {
		counts[l.SessionID]++
		if !sessions[l.SessionID] {
			add(MissingReference{Kind: LinkKind, ID: l.Handle().target(), ReferencedKind: SessionKind, ReferencedID: string(l.SessionID), Reason: "missing session"})
		}
		if !windows[l.WindowID] {
			add(MissingReference{Kind: LinkKind, ID: l.Handle().target(), ReferencedKind: WindowKind, ReferencedID: string(l.WindowID), Reason: "missing window"})
		}
	}
	for _, p := range v.panes {
		paneCounts[p.WindowID]++
		if !windows[p.WindowID] {
			add(MissingReference{Kind: PaneKind, ID: string(p.ID), ReferencedKind: WindowKind, ReferencedID: string(p.WindowID), Reason: "missing window"})
		}
	}
	for _, s := range v.sessions {
		if counts[s.ID] != s.WindowCount {
			add(MissingReference{Kind: SessionKind, ID: string(s.ID), Reason: "observed window count differs from membership count"})
		}
	}
	for _, w := range v.windows {
		if paneCounts[w.ID] != w.PaneCount {
			add(MissingReference{Kind: WindowKind, ID: string(w.ID), Reason: "observed pane count differs from collected panes"})
		}
	}
	for _, c := range v.clients {
		if sid, ok := c.SessionID.Get(); ok && !sessions[sid] {
			add(MissingReference{Kind: ClientKind, ID: string(c.Name), ReferencedKind: SessionKind, ReferencedID: string(sid), Reason: "missing client session"})
		}
	}
}
