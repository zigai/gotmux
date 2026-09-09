package tmux

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/zigai/gotmux/internal/codec"
)

const (
	paneOutputEventBaseOverhead    int64 = 96
	layoutChangedEventBaseOverhead int64 = 96
	sessionEventBaseOverhead       int64 = 96
	subscriptionEventBaseOverhead  int64 = 128
	unknownEventBaseOverhead       int64 = 64
)

// Event is the common interface implemented by all tmux control mode notifications.
// The interface is sealed and cannot be implemented outside this package.
type Event interface {
	// RawName returns the notification name reported by tmux without the leading percent sign
	// (e.g. "output", "window-add", "session-changed").
	RawName() string

	// Received returns the local timestamp when the event was read from the control wire.
	Received() time.Time

	event()
	eventBytes() int64
	cloneEvent() Event
}

type eventBase struct {
	name     string
	received time.Time
}

// PaneOutputEvent reports terminal output emitted by a process running in a pane (%output).
type PaneOutputEvent struct {
	eventBase

	// PaneID is the ID of the pane that produced the output.
	PaneID PaneID

	// Age is the age reported by extended-output (%extended-output), if available.
	Age Value[time.Duration]

	data []byte
}

// LayoutChangedEvent reports a change to a window's pane geometry or zoom state (%layout-change).
type LayoutChangedEvent struct {
	eventBase

	// WindowID is the ID of the window whose layout changed.
	WindowID WindowID

	// Layout is the new serialized layout geometry.
	Layout Layout

	// VisibleLayout is the visible layout description if different.
	VisibleLayout Layout

	// Flags contains window status flags.
	Flags string
}

// SessionEvent reports a session lifecycle notification (e.g. %session-changed, %session-renamed).
type SessionEvent struct {
	eventBase

	// SessionID is the target session ID, if reported.
	SessionID Value[SessionID]

	// Name is the session name, if reported.
	Name Value[string]

	payload string
}

// WindowEvent reports a window lifecycle notification (e.g. %window-add, %window-close, %window-renamed).
type WindowEvent struct {
	eventBase

	// WindowID is the target window ID.
	WindowID WindowID

	// Name is the window name, if reported.
	Name Value[string]

	payload string
}

// ClientEvent reports a client attachment or detachment notification (%client-session-changed, %client-detached).
type ClientEvent struct {
	eventBase

	// ClientName is the name of the affected client terminal.
	ClientName ClientName

	// SessionID is the session the client switched to or detached from.
	SessionID Value[SessionID]

	payload string
}

// SubscriptionEvent reports a format value change notification (%subscription-changed)
// for a watch registered via [Connection.WatchFormat].
type SubscriptionEvent struct {
	eventBase

	// Name is the subscription name provided when registered.
	Name string

	// SessionID, WindowID, PaneID are the target entity identifiers associated with the subscription.
	SessionID Value[SessionID]
	WindowID  Value[WindowID]
	PaneID    Value[PaneID]

	data []byte

	// RawHeader is the verbatim header text emitted by tmux.
	RawHeader string
}

// UnknownEvent represents an unrecognized notification frame from the tmux control wire.
type UnknownEvent struct {
	eventBase

	payload []byte
}

func (e eventBase) RawName() string     { return e.name }
func (e eventBase) Received() time.Time { return e.received }
func (eventBase) event()                {}

// Data returns a defensive copy of the raw terminal output bytes emitted by the pane.
func (e PaneOutputEvent) Data() []byte { return bytes.Clone(e.data) }

func (e PaneOutputEvent) eventBytes() int64 {
	return int64(len(e.data)+len(e.name)) + paneOutputEventBaseOverhead
}
func (e PaneOutputEvent) cloneEvent() Event { e.data = bytes.Clone(e.data); return e }

func (e LayoutChangedEvent) eventBytes() int64 {
	return int64(len(e.name)+len(e.Layout)+len(e.VisibleLayout)+len(e.Flags)) + layoutChangedEventBaseOverhead
}
func (e LayoutChangedEvent) cloneEvent() Event { return e }

func (e SessionEvent) eventBytes() int64 {
	return int64(len(e.name)+len(e.payload)) + sessionEventBaseOverhead
}
func (e SessionEvent) cloneEvent() Event { return e }

func (e WindowEvent) eventBytes() int64 {
	return int64(len(e.name)+len(e.payload)) + sessionEventBaseOverhead
}
func (e WindowEvent) cloneEvent() Event { return e }

func (e ClientEvent) eventBytes() int64 {
	return int64(len(e.name)+len(e.payload)) + sessionEventBaseOverhead
}
func (e ClientEvent) cloneEvent() Event { return e }

// Data returns a defensive copy of the evaluated format expression bytes.
func (e SubscriptionEvent) Data() []byte { return bytes.Clone(e.data) }

func (e SubscriptionEvent) eventBytes() int64 {
	return int64(len(e.name)+len(e.Name)+len(e.RawHeader)+len(e.data)) + subscriptionEventBaseOverhead
}
func (e SubscriptionEvent) cloneEvent() Event { e.data = bytes.Clone(e.data); return e }

// Payload returns a defensive copy of the raw notification payload bytes.
func (e UnknownEvent) Payload() []byte { return bytes.Clone(e.payload) }

func (e UnknownEvent) eventBytes() int64 {
	return int64(len(e.name)+len(e.payload)) + unknownEventBaseOverhead
}
func (e UnknownEvent) cloneEvent() Event { e.payload = bytes.Clone(e.payload); return e }
func decodeEvent(line []byte, maxBytes int64) (Event, error) {
	if !validateEventLine(line, maxBytes) {
		return nil, ErrProtocol
	}

	s := strings.TrimSuffix(string(line), "\n")

	name, rest, _ := strings.Cut(s[1:], " ")
	if name == "" {
		return nil, ErrProtocol
	}

	base := eventBase{name: name, received: time.Now()}

	switch {
	case isOutputEvent(name):
		return decodeOutputEvent(base, name, rest, maxBytes)
	case name == "layout-change":
		return decodeLayoutEvent(base, rest)
	case isSessionEvent(name):
		return decodeSessionEvent(base, name, rest)
	case isWindowEvent(name):
		return decodeWindowEvent(base, rest)
	case isClientEvent(name):
		return decodeClientEvent(base, name, rest)
	case name == "subscription-changed":
		return decodeSubscriptionEvent(base, rest)
	case isValidEventName(name):
		return UnknownEvent{eventBase: base, payload: []byte(rest)}, nil
	default:
		return nil, ErrProtocol
	}
}

func validateEventLine(line []byte, maxBytes int64) bool {
	return int64(len(line)) <= maxBytes && len(line) >= 2 && line[0] == '%'
}

func isOutputEvent(name string) bool {
	return name == "output" || name == "extended-output"
}

func isClientEvent(name string) bool {
	return name == "client-session-changed" || name == "client-detached"
}

func isSessionEvent(name string) bool {
	switch name {
	case "session-changed", "session-renamed", "session-window-changed", "sessions-changed":
		return true
	default:
		return false
	}
}

func isWindowEvent(name string) bool {
	switch name {
	case "window-add", "window-close", "window-renamed", "unlinked-window-add", "unlinked-window-close", "unlinked-window-renamed":
		return true
	default:
		return false
	}
}

func isValidEventName(name string) bool {
	for _, b := range []byte(name) {
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '-' {
			return false
		}
	}

	return true
}

func decodeOutputEvent(base eventBase, name, rest string, maxBytes int64) (Event, error) {
	id, tail, ok := strings.Cut(rest, " ")
	if !ok || !PaneID(id).Valid() {
		return nil, ErrProtocol
	}

	age := UnavailableValue[time.Duration]()

	if name == "extended-output" {
		head, data, cutOk := strings.Cut(tail, " : ")
		if !cutOk {
			return nil, ErrProtocol
		}

		parts := strings.Fields(head)
		if len(parts) < 1 {
			return nil, ErrProtocol
		}

		n, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || n < 0 || n > int64((1<<63-1)/time.Millisecond) {
			return nil, ErrProtocol
		}

		age = PresentValue(time.Duration(n) * time.Millisecond)
		tail = data
	}

	data, err := codec.Octal([]byte(tail), maxBytes)
	if err != nil {
		return nil, ErrProtocol
	}

	return PaneOutputEvent{eventBase: base, PaneID: PaneID(id), Age: age, data: data}, nil
}

func decodeLayoutEvent(base eventBase, rest string) (Event, error) {
	fields := strings.Fields(rest)
	if len(fields) != 4 || !WindowID(fields[0]).Valid() {
		return nil, ErrProtocol
	}

	return LayoutChangedEvent{
		eventBase:     base,
		WindowID:      WindowID(fields[0]),
		Layout:        Layout(fields[1]),
		VisibleLayout: Layout(fields[2]),
		Flags:         fields[3],
	}, nil
}

func decodeSessionEvent(base eventBase, name, rest string) (Event, error) {
	if name == "sessions-changed" {
		return SessionEvent{
			eventBase: base,
			SessionID: UnavailableValue[SessionID](),
			Name:      UnavailableValue[string](),
			payload:   rest,
		}, nil
	}

	id, tail, _ := strings.Cut(rest, " ")
	if !SessionID(id).Valid() {
		return nil, ErrProtocol
	}

	sessionName := UnavailableValue[string]()
	if name != "session-window-changed" {
		sessionName = PresentValue(tail)
	}

	return SessionEvent{
		eventBase: base,
		SessionID: PresentValue(SessionID(id)),
		Name:      sessionName,
		payload:   rest,
	}, nil
}

func decodeWindowEvent(base eventBase, rest string) (Event, error) {
	id, tail, has := strings.Cut(rest, " ")
	if !WindowID(id).Valid() {
		return nil, ErrProtocol
	}

	windowName := UnavailableValue[string]()
	if has {
		windowName = PresentValue(tail)
	}

	return WindowEvent{
		eventBase: base,
		WindowID:  WindowID(id),
		Name:      windowName,
		payload:   rest,
	}, nil
}

func decodeClientEvent(base eventBase, name, rest string) (Event, error) {
	id, tail, _ := strings.Cut(rest, " ")
	if !ClientName(id).Valid() {
		return nil, ErrProtocol
	}

	sessionID := UnavailableValue[SessionID]()

	if name == "client-session-changed" {
		sid, _, _ := strings.Cut(tail, " ")
		if !SessionID(sid).Valid() {
			return nil, ErrProtocol
		}

		sessionID = PresentValue(SessionID(sid))
	}

	return ClientEvent{
		eventBase:  base,
		ClientName: ClientName(id),
		SessionID:  sessionID,
		payload:    rest,
	}, nil
}

func decodeSubscriptionEvent(base eventBase, rest string) (Event, error) {
	header, data, ok := strings.Cut(rest, " : ")
	if !ok {
		return nil, ErrProtocol
	}

	parts := strings.Fields(header)
	if len(parts) < 1 {
		return nil, ErrProtocol
	}

	sid := UnavailableValue[SessionID]()
	wid := UnavailableValue[WindowID]()
	pid := UnavailableValue[PaneID]()

	for _, x := range parts[1:] {
		if SessionID(x).Valid() {
			sid = PresentValue(SessionID(x))
		}

		if WindowID(x).Valid() {
			wid = PresentValue(WindowID(x))
		}

		if PaneID(x).Valid() {
			pid = PresentValue(PaneID(x))
		}
	}

	return SubscriptionEvent{
		eventBase: base,
		Name:      parts[0],
		RawHeader: header,
		SessionID: sid,
		WindowID:  wid,
		PaneID:    pid,
		data:      []byte(data),
	}, nil
}
