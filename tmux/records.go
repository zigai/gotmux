package tmux

import "time"

type rawRecord struct{ raw map[string]string }

// ServerInfo captures daemon-level metadata returned by [Server.Probe].
type ServerInfo struct {
	rawRecord

	// Identity is the verified runtime identity of the answering daemon.
	Identity ServerIdentity

	// Version is the tmux version string reported by the daemon.
	Version Version
}

// SessionInfo captures a point-in-time snapshot of a tmux session's metadata.
// It is plain data and does not refresh automatically.
type SessionInfo struct {
	rawRecord

	// ID is the immutable canonical session ID (e.g. "$0").
	ID SessionID

	// Name is the human-readable session name (#{session_name}).
	Name string

	// Path is the initial working directory configured for this session (#{session_path}).
	Path string

	// Created is the timestamp when the session was created (#{session_created}).
	Created time.Time

	// Activity is the timestamp of the most recent activity in this session (#{session_activity}).
	Activity time.Time

	// Attached is the count of client terminals currently attached to this session.
	Attached int

	// WindowCount is the number of windows linked into this session.
	WindowCount int

	// Group is the session group name if this session belongs to a group.
	Group Value[string]

	h handle
}

// WindowInfo captures a point-in-time snapshot of a tmux window's metadata.
type WindowInfo struct {
	rawRecord

	// ID is the immutable canonical window ID (e.g. "@0").
	ID WindowID

	// Name is the window name (#{window_name}).
	Name string

	// Width is the window width in terminal columns (#{window_width}).
	Width int

	// Height is the window height in terminal rows (#{window_height}).
	Height int

	// PaneCount is the number of panes currently inside this window.
	PaneCount int

	// Layout is the serialized layout geometry description for this window's panes.
	Layout Layout

	// Zoomed indicates whether a pane in this window is currently zoomed to fill the window.
	Zoomed bool

	h handle
}

// WindowLinkInfo captures metadata for a window linked into a specific session slot.
type WindowLinkInfo struct {
	rawRecord

	// SessionID is the parent session ID containing this link.
	SessionID SessionID

	// WindowID is the target window ID linked at this slot.
	WindowID WindowID

	// WindowName is the human-readable name of the window linked at this slot.
	WindowName string
	// Index is the window index within the session (e.g. 0 for session:0).
	Index int

	// Active indicates whether this is the currently focused window in the session.
	Active bool

	// Flags contains tmux status flags for this window (e.g. "*", "-", "#").
	Flags string

	link WindowLink
}

// SelectionInfo preserves tmux's copy-mode coordinates and selection state.
// These are raw buffer coordinates; they do not claim to be capture-relative positions.
type SelectionInfo struct {
	// StartX, StartY are the 0-based coordinates where selection began.
	StartX, StartY int

	// EndX, EndY are the current 0-based coordinates of the selection endpoint.
	EndX, EndY int

	// Rectangle indicates whether rectangular block selection is active.
	Rectangle bool

	// ScrollPosition is the scrollback offset in lines from the bottom.
	ScrollPosition int
}

// PaneInfo captures a point-in-time snapshot of a tmux pane's metadata.
type PaneInfo struct {
	rawRecord

	// ID is the immutable canonical pane ID (e.g. "%0").
	ID PaneID

	// WindowID is the ID of the window containing this pane.
	WindowID WindowID

	// SessionID is the parent session ID containing this pane, if reported.
	SessionID Value[SessionID]

	// SessionName is the parent session name containing this pane, if reported.
	SessionName Value[string]

	// WindowName is the parent window name containing this pane, if reported.
	WindowName Value[string]

	// WindowIndex is the parent window index containing this pane, if reported.
	WindowIndex Value[int]
	// Index is the 0-based pane index within its window (#{pane_index}).
	Index int

	// Title is the pane title string (#{pane_title}).
	Title string

	// CurrentPath is the working directory reported by tmux for this pane (#{pane_current_path}).
	CurrentPath string

	// CurrentCommand is the name of the foreground process running in this pane (#{pane_current_command}).
	CurrentCommand string

	// PID is the operating system process ID of the direct child process in this pane (#{pane_pid}).
	PID int

	// TTY is the pseudo-terminal device path assigned to this pane (#{pane_tty}).
	TTY string

	// Width is the pane width in terminal columns (#{pane_width}).
	Width int

	// Height is the pane height in terminal rows (#{pane_height}).
	Height int

	// Left is the 0-based column offset of this pane's top-left corner within the window.
	Left int

	// Top is the 0-based row offset of this pane's top-left corner within the window.
	Top int

	// Active indicates whether this pane currently has focus within its window.
	Active bool

	// HistorySize is the number of scrollback lines currently retained for this pane.
	HistorySize int

	// Alternate indicates whether the terminal is currently rendering on the alternate screen buffer.
	Alternate bool

	// Dead indicates whether the pane's process has terminated (#{pane_dead}).
	Dead bool

	// DeadStatus is the process exit code if Dead is true (#{pane_dead_status}).
	DeadStatus Value[int]

	// CursorX, CursorY are the 0-based cursor coordinates within the pane.
	CursorX int
	CursorY int

	// Mode is the active mode name (e.g. "copy-mode") if a mode is installed.
	Mode Value[string]

	// Selection holds copy-mode selection bounds if a selection is active.
	Selection Value[SelectionInfo]

	h handle
}

// ClientInfo captures a point-in-time snapshot of an attached tmux client terminal.
type ClientInfo struct {
	rawRecord

	// Name is the client terminal identifier (e.g. "/dev/pts/1").
	Name ClientName

	// TTY is the client's controlling terminal path.
	TTY string

	// PID is the process ID of the tmux client subprocess.
	PID int

	// Created is the timestamp when the client connected.
	Created time.Time

	// Activity is the timestamp of the most recent user interaction from this client.
	Activity time.Time

	// Width, Height are terminal dimensions in cells; zero means unavailable for control clients.
	Width  int
	Height int

	// SessionID is the session currently displayed on this client.
	SessionID Value[SessionID]

	// Control indicates whether this client is running in tmux control mode (-C).
	Control bool

	// ReadOnly indicates whether the client is in read-only mode (-r).
	ReadOnly bool

	// Flags contains client status flags reported by tmux (#{client_flags}).
	Flags []string

	h handle
}

// Raw returns an owned byte copy of a requested format variable.
// Returns false if the format variable was not requested via [QueryOptions.ExtraFields]
// or was absent from the response. (Distinguishable from a present empty string).
func (r rawRecord) Raw(name string) ([]byte, bool) {
	s, ok := r.raw[name]
	if !ok {
		return nil, false
	}

	return []byte(s), true
}

// Handle returns a [Session] handle with provenance tied to the daemon where this record was observed.
func (v SessionInfo) Handle() Session { return Session{h: v.h} }

// Handle returns a [Window] handle with provenance tied to the daemon where this record was observed.
func (v WindowInfo) Handle() Window { return Window{h: v.h} }

// Handle returns a [WindowLink] handle for this session slot with preserved daemon provenance.
func (v WindowLinkInfo) Handle() WindowLink { return v.link }

// Handle returns a [Pane] handle with provenance tied to the daemon where this record was observed.
func (v PaneInfo) Handle() Pane { return Pane{h: v.h} }

// Handle returns a [Client] handle with provenance tied to the daemon where this record was observed.
func (v ClientInfo) Handle() Client { return Client{h: v.h} }
