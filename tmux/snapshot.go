package tmux

import (
	"context"
	"time"
)

const (
	// Consistent indicates all observed cross-references resolved without contradiction (not an atomic transaction).
	Consistent Consistency = iota

	// Incomplete indicates concurrent graph churn occurred during collection, leaving dangling references.
	Incomplete
)

const maxMissingReferences = 128

// Consistency indicates whether cross-references within a snapshot resolved cleanly.
type Consistency uint8

// MissingReference describes a dangling reference detected during snapshot validation.
type MissingReference struct {
	// Kind is the type of object holding the dangling reference (e.g. PaneKind).
	Kind ObjectKind

	// ID is the identifier of the object holding the dangling reference.
	ID string

	// ReferencedKind is the expected target object type that was not found.
	ReferencedKind ObjectKind

	// ReferencedID is the identifier of the missing target object.
	ReferencedID string

	// Reason explains why resolution failed.
	Reason string
}

// Snapshot represents a point-in-time observation of all entities on a tmux server.
// Accessors perform no I/O and return owned copies.
//
//nolint:recvcheck // assess is an internal constructor step mutating unexported fields, while public accessors use value receivers for immutability.
type Snapshot struct {
	// Started is the timestamp when snapshot collection began.
	Started time.Time

	// Finished is the timestamp when snapshot collection completed.
	Finished time.Time

	// Identity is the verified server identity where this snapshot was collected.
	Identity ServerIdentity

	// Consistency reports whether all cross-entity references resolved cleanly.
	Consistency Consistency

	// MissingCount is the total count of unresolved references detected during validation.
	MissingCount int

	sessions []SessionInfo
	windows  []WindowInfo
	links    []WindowLinkInfo
	panes    []PaneInfo
	clients  []ClientInfo
	missing  []MissingReference
}

// Sessions returns a copy of all sessions observed in this snapshot.
func (v Snapshot) Sessions() []SessionInfo { return append([]SessionInfo{}, v.sessions...) }

// Windows returns a copy of all deduplicated windows observed in this snapshot.
func (v Snapshot) Windows() []WindowInfo { return append([]WindowInfo{}, v.windows...) }

// Links returns a copy of all window link slots observed in this snapshot.
func (v Snapshot) Links() []WindowLinkInfo { return append([]WindowLinkInfo{}, v.links...) }

// Panes returns a copy of all panes observed in this snapshot.
func (v Snapshot) Panes() []PaneInfo { return append([]PaneInfo{}, v.panes...) }

// Clients returns a copy of all client terminals observed in this snapshot.
func (v Snapshot) Clients() []ClientInfo {
	out := append([]ClientInfo{}, v.clients...)
	for i := range out {
		out[i].Flags = append([]string{}, out[i].Flags...)
	}

	return out
}

// MissingReferences returns a slice of up to 128 dangling references detected during
// snapshot graph validation if Consistency is [Incomplete].
func (v Snapshot) MissingReferences() []MissingReference {
	return append([]MissingReference{}, v.missing...)
}

// Pane looks up a pane's point-in-time metadata by its ID in the snapshot.
func (v Snapshot) Pane(id PaneID) (PaneInfo, bool) {
	for _, p := range v.panes {
		if p.h.id == string(id) {
			return p, true
		}
	}

	var zero PaneInfo

	return zero, false
}

// Window looks up a window's point-in-time metadata by its ID in the snapshot.
func (v Snapshot) Window(id WindowID) (WindowInfo, bool) {
	for _, w := range v.windows {
		if w.h.id == string(id) {
			return w, true
		}
	}

	var zero WindowInfo

	return zero, false
}

// Session looks up a session's point-in-time metadata by its ID in the snapshot.
func (v Snapshot) Session(id SessionID) (SessionInfo, bool) {
	for _, s := range v.sessions {
		if s.h.id == string(id) {
			return s, true
		}
	}

	var zero SessionInfo

	return zero, false
}

// Client looks up an attached client terminal's point-in-time metadata by its name in the snapshot.
func (v Snapshot) Client(name ClientName) (ClientInfo, bool) {
	for _, c := range v.clients {
		if c.h.id == string(name) {
			c.Flags = append([]string{}, c.Flags...)
			return c, true
		}
	}

	var zero ClientInfo

	return zero, false
}

// ResolvePane looks up a pane by ID and returns an executable [Pane] handle with daemon provenance.
func (v Snapshot) ResolvePane(id PaneID) (Pane, bool) { p, ok := v.Pane(id); return p.Handle(), ok }

// ResolveWindow looks up a window by ID and returns an executable [Window] handle with daemon provenance.
func (v Snapshot) ResolveWindow(id WindowID) (Window, bool) {
	w, ok := v.Window(id)
	return w.Handle(), ok
}

// ResolveSession looks up a session by ID and returns an executable [Session] handle with daemon provenance.
func (v Snapshot) ResolveSession(id SessionID) (Session, bool) {
	s, ok := v.Session(id)
	return s.Handle(), ok
}

// ResolveClient looks up a client by name and returns an executable [Client] handle with daemon provenance.
func (v Snapshot) ResolveClient(name ClientName) (Client, bool) {
	c, ok := v.Client(name)
	return c.Handle(), ok
}

// Snapshot collects a point-in-time observation of the entire daemon's sessions,
// windows, links, panes, and clients in a small number of batched queries.
//
// It validates cross-entity references and marks the snapshot [Consistent] or [Incomplete].
func (s *Server) Snapshot(ctx context.Context) (Snapshot, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return Snapshot{}, opError("Snapshot", err)
	}
	defer op.close()

	var out Snapshot

	out.Started = time.Now()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return Snapshot{}, opError("Snapshot", err)
	}

	out.Identity = info.Identity

	out.sessions, err = s.sessions(opCtx, op, info.Identity, QueryOptions{Filter: "", ExtraFields: nil})
	if err != nil {
		return Snapshot{}, opError("Snapshot", err)
	}

	out.windows, out.links, err = s.windows(opCtx, op, info.Identity, "")
	if err != nil {
		return Snapshot{}, opError("Snapshot", err)
	}

	out.panes, err = s.panes(opCtx, op, info.Identity, QueryOptions{Filter: "", ExtraFields: nil}, "")
	if err != nil {
		return Snapshot{}, opError("Snapshot", err)
	}

	out.clients, err = s.clients(opCtx, op, info.Identity)
	if err != nil {
		return Snapshot{}, opError("Snapshot", err)
	}

	if err = opCtx.Err(); err != nil {
		return Snapshot{}, afterError("Snapshot", err)
	}

	out.Finished = time.Now()
	out.assess()

	return out, nil
}

func (v *Snapshot) checkLinkReferences(sessions map[SessionID]bool, windows map[WindowID]bool, counts map[SessionID]int, add func(MissingReference)) {
	for _, l := range v.links {
		counts[l.SessionID]++
		if !sessions[l.SessionID] {
			add(MissingReference{Kind: LinkKind, ID: l.Handle().target(), ReferencedKind: SessionKind, ReferencedID: string(l.SessionID), Reason: "missing session"})
		}

		if !windows[l.WindowID] {
			add(MissingReference{Kind: LinkKind, ID: l.Handle().target(), ReferencedKind: WindowKind, ReferencedID: string(l.WindowID), Reason: "missing window"})
		}
	}
}

func (v *Snapshot) checkPaneReferences(windows map[WindowID]bool, paneCounts map[WindowID]int, add func(MissingReference)) {
	for _, p := range v.panes {
		paneCounts[p.WindowID]++
		if !windows[p.WindowID] {
			add(MissingReference{Kind: PaneKind, ID: string(p.ID), ReferencedKind: WindowKind, ReferencedID: string(p.WindowID), Reason: "missing window"})
		}
	}
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
		if len(v.missing) < maxMissingReferences {
			v.missing = append(v.missing, m)
		}
	}

	v.checkLinkReferences(sessions, windows, counts, add)
	v.checkPaneReferences(windows, paneCounts, add)

	for _, s := range v.sessions {
		if counts[s.ID] != s.WindowCount {
			add(MissingReference{
				Kind:           SessionKind,
				ID:             string(s.ID),
				ReferencedKind: ObjectKind(""),
				ReferencedID:   "",
				Reason:         "observed window count differs from membership count",
			})
		}
	}

	for _, w := range v.windows {
		if paneCounts[w.ID] != w.PaneCount {
			add(MissingReference{
				Kind:           WindowKind,
				ID:             string(w.ID),
				ReferencedKind: ObjectKind(""),
				ReferencedID:   "",
				Reason:         "observed pane count differs from collected panes",
			})
		}
	}

	for _, c := range v.clients {
		if sid, ok := c.SessionID.Get(); ok && !sessions[sid] {
			add(MissingReference{Kind: ClientKind, ID: string(c.Name), ReferencedKind: SessionKind, ReferencedID: string(sid), Reason: "missing client session"})
		}
	}
}
