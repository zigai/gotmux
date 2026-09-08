package tmux

import "time"

type rawRecord struct{ raw map[string]string }

// Raw returns an owned byte copy; absence differs from an empty expansion.
func (r rawRecord) Raw(name string) ([]byte, bool) {
	s, ok := r.raw[name]
	if !ok {
		return nil, false
	}
	return []byte(s), true
}

type ServerInfo struct {
	Identity ServerIdentity
	Version  Version
	rawRecord
}
type SessionInfo struct {
	ID          SessionID
	Name        string
	Path        string
	Created     time.Time
	Activity    time.Time
	Attached    int
	WindowCount int
	Group       Value[string]
	rawRecord
	h handle
}

func (v SessionInfo) Handle() Session { return Session{h: v.h} }

type WindowInfo struct {
	ID        WindowID
	Name      string
	Width     int
	Height    int
	PaneCount int
	Layout    Layout
	Zoomed    bool
	rawRecord
	h handle
}

func (v WindowInfo) Handle() Window { return Window{h: v.h} }

type WindowLinkInfo struct {
	SessionID SessionID
	WindowID  WindowID
	Index     int
	Active    bool
	Flags     string
	rawRecord
	link WindowLink
}

func (v WindowLinkInfo) Handle() WindowLink { return v.link }

// SelectionInfo preserves tmux's copy-mode coordinates. It does not claim that
// these are capture-relative positions or that a selection is a stable quote.
type SelectionInfo struct {
	StartX, StartY, EndX, EndY int
	Rectangle                  bool
	ScrollPosition             int
}
type PaneInfo struct {
	ID             PaneID
	WindowID       WindowID
	Index          int
	Title          string
	CurrentPath    string
	CurrentCommand string
	PID            int
	TTY            string
	Width          int
	Height         int
	Left           int
	Top            int
	Active         bool
	HistorySize    int
	Alternate      bool
	Dead           bool
	DeadStatus     Value[int]
	CursorX        int
	CursorY        int
	Mode           Value[string]
	Selection      Value[SelectionInfo]
	rawRecord
	h handle
}

func (v PaneInfo) Handle() Pane { return Pane{h: v.h} }

type ClientInfo struct {
	Name      ClientName
	TTY       string
	PID       int
	Created   time.Time
	Activity  time.Time
	Width     int
	Height    int
	SessionID Value[SessionID]
	Control   bool
	ReadOnly  bool
	Flags     []string
	rawRecord
	h handle
}

func (v ClientInfo) Handle() Client { return Client{h: v.h} }
