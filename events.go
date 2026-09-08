package tmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"example.com/tmux/internal/codec"
)

// Event is closed to implementations outside this package. RawName preserves
// the exact notification name, without the leading percent sign.
type Event interface {
	RawName() string
	event()
	eventBytes() int64
	cloneEvent() Event
}
type eventBase struct {
	name     string
	received time.Time
}

func (e eventBase) RawName() string     { return e.name }
func (e eventBase) Received() time.Time { return e.received }
func (eventBase) event()                {}

type PaneOutputEvent struct {
	eventBase
	PaneID PaneID
	Age    Value[time.Duration]
	data   []byte
}

func (e PaneOutputEvent) Data() []byte      { return bytes.Clone(e.data) }
func (e PaneOutputEvent) eventBytes() int64 { return int64(len(e.data) + len(e.name) + 96) }
func (e PaneOutputEvent) cloneEvent() Event { e.data = bytes.Clone(e.data); return e }

type LayoutChangedEvent struct {
	eventBase
	WindowID      WindowID
	Layout        Layout
	VisibleLayout Layout
	Flags         string
}

func (e LayoutChangedEvent) eventBytes() int64 {
	return int64(len(e.name) + len(e.Layout) + len(e.VisibleLayout) + len(e.Flags) + 96)
}
func (e LayoutChangedEvent) cloneEvent() Event { return e }

type SessionEvent struct {
	eventBase
	SessionID Value[SessionID]
	Name      Value[string]
	payload   string
}

func (e SessionEvent) eventBytes() int64 { return int64(len(e.name) + len(e.payload) + 96) }
func (e SessionEvent) cloneEvent() Event { return e }

type WindowEvent struct {
	eventBase
	WindowID WindowID
	Name     Value[string]
	payload  string
}

func (e WindowEvent) eventBytes() int64 { return int64(len(e.name) + len(e.payload) + 96) }
func (e WindowEvent) cloneEvent() Event { return e }

type ClientEvent struct {
	eventBase
	ClientName ClientName
	SessionID  Value[SessionID]
	payload    string
}

func (e ClientEvent) eventBytes() int64 { return int64(len(e.name) + len(e.payload) + 96) }
func (e ClientEvent) cloneEvent() Event { return e }

type SubscriptionEvent struct {
	eventBase
	Name      string
	SessionID Value[SessionID]
	WindowID  Value[WindowID]
	PaneID    Value[PaneID]
	data      []byte
	RawHeader string
}

func (e SubscriptionEvent) Data() []byte { return bytes.Clone(e.data) }
func (e SubscriptionEvent) eventBytes() int64 {
	return int64(len(e.name) + len(e.Name) + len(e.RawHeader) + len(e.data) + 128)
}
func (e SubscriptionEvent) cloneEvent() Event { e.data = bytes.Clone(e.data); return e }

type UnknownEvent struct {
	eventBase
	payload []byte
}

func (e UnknownEvent) Payload() []byte   { return bytes.Clone(e.payload) }
func (e UnknownEvent) eventBytes() int64 { return int64(len(e.name) + len(e.payload) + 64) }
func (e UnknownEvent) cloneEvent() Event { e.payload = bytes.Clone(e.payload); return e }

func decodeEvent(line []byte, maxBytes int64) (Event, error) {
	if int64(len(line)) > maxBytes || len(line) < 2 || line[0] != '%' {
		return nil, ErrProtocol
	}
	s := strings.TrimSuffix(string(line), "\n")
	name, rest, _ := strings.Cut(s[1:], " ")
	base := eventBase{name: name, received: time.Now()}
	if name == "" {
		return nil, ErrProtocol
	}
	switch name {
	case "output", "extended-output":
		id, tail, ok := strings.Cut(rest, " ")
		if !ok || !PaneID(id).Valid() {
			return nil, ErrProtocol
		}
		age := UnavailableValue[time.Duration]()
		if name == "extended-output" {
			head, data, ok := strings.Cut(tail, " : ")
			if !ok {
				return nil, ErrProtocol
			}
			parts := strings.Fields(head)
			if len(parts) < 1 {
				return nil, ErrProtocol
			}
			n, e := strconv.ParseInt(parts[0], 10, 64)
			if e != nil || n < 0 || n > int64((1<<63-1)/time.Millisecond) {
				return nil, ErrProtocol
			}
			age = PresentValue(time.Duration(n) * time.Millisecond)
			tail = data
		}
		data, e := codec.Octal([]byte(tail), maxBytes)
		if e != nil {
			return nil, ErrProtocol
		}
		return PaneOutputEvent{eventBase: base, PaneID: PaneID(id), Age: age, data: data}, nil
	case "layout-change":
		fields := strings.Fields(rest)
		if len(fields) != 4 || !WindowID(fields[0]).Valid() {
			return nil, ErrProtocol
		}
		return LayoutChangedEvent{eventBase: base, WindowID: WindowID(fields[0]), Layout: Layout(fields[1]), VisibleLayout: Layout(fields[2]), Flags: fields[3]}, nil
	case "session-changed", "session-renamed", "session-window-changed":
		id, tail, _ := strings.Cut(rest, " ")
		if !SessionID(id).Valid() {
			return nil, ErrProtocol
		}
		v := SessionEvent{eventBase: base, SessionID: PresentValue(SessionID(id)), payload: rest}
		if name != "session-window-changed" {
			v.Name = PresentValue(tail)
		}
		return v, nil
	case "sessions-changed":
		return SessionEvent{eventBase: base, payload: rest}, nil
	case "window-add", "window-close", "window-renamed", "unlinked-window-add", "unlinked-window-close", "unlinked-window-renamed":
		id, tail, has := strings.Cut(rest, " ")
		if !WindowID(id).Valid() {
			return nil, ErrProtocol
		}
		v := WindowEvent{eventBase: base, WindowID: WindowID(id), payload: rest}
		if has {
			v.Name = PresentValue(tail)
		}
		return v, nil
	case "client-session-changed", "client-detached":
		id, tail, _ := strings.Cut(rest, " ")
		if !ClientName(id).Valid() {
			return nil, ErrProtocol
		}
		v := ClientEvent{eventBase: base, ClientName: ClientName(id), payload: rest}
		if name == "client-session-changed" {
			sid, _, _ := strings.Cut(tail, " ")
			if !SessionID(sid).Valid() {
				return nil, ErrProtocol
			}
			v.SessionID = PresentValue(SessionID(sid))
		}
		return v, nil
	case "subscription-changed":
		header, data, ok := strings.Cut(rest, " : ")
		if !ok {
			return nil, ErrProtocol
		}
		parts := strings.Fields(header)
		if len(parts) < 1 {
			return nil, ErrProtocol
		}
		// Keep the full tmux header: version-specific subscription indexes have
		// meaning beyond the convenience fields, and are never silently discarded.
		v := SubscriptionEvent{eventBase: base, Name: parts[0], RawHeader: header, data: []byte(data)}
		for _, x := range parts[1:] {
			if SessionID(x).Valid() {
				v.SessionID = PresentValue(SessionID(x))
			}
			if WindowID(x).Valid() {
				v.WindowID = PresentValue(WindowID(x))
			}
			if PaneID(x).Valid() {
				v.PaneID = PresentValue(PaneID(x))
			}
		}
		return v, nil
	default:
		for _, b := range []byte(name) {
			if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
				return nil, ErrProtocol
			}
		}
		return UnknownEvent{eventBase: base, payload: []byte(rest)}, nil
	}
}

type OverflowPolicy uint8

const (
	FailOnOverflow OverflowPolicy = iota
)

type EventOptions struct {
	MaxBytes int64
	MaxCount int
	Overflow OverflowPolicy
}
type EventStream struct {
	conn        *Connection
	mu          sync.Mutex
	queue       []Event
	bytes       int64
	maxBytes    int64
	maxCount    int
	terminal    error
	wake        chan struct{}
	done        chan struct{}
	reading     atomic.Bool
	releaseOnce sync.Once
}

func normalizeEventOptions(o EventOptions) (EventOptions, error) {
	if o.MaxBytes < 0 || o.MaxCount < 0 || o.Overflow != FailOnOverflow {
		return o, invalid("event options")
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = 4 << 20
	}
	if o.MaxCount == 0 {
		o.MaxCount = 256
	}
	if o.MaxBytes > 1<<40 || o.MaxCount > 1<<20 {
		return o, invalid("event limits")
	}
	return o, nil
}

// Events reserves the stream's full byte capacity immediately. Its context owns
// the stream lifetime. Closing a stream never closes its connection.
func (c *Connection) Events(ctx context.Context, o EventOptions) (*EventStream, error) {
	if ctx == nil {
		return nil, opError("Events", invalid("nil context"))
	}
	if e := ctx.Err(); e != nil {
		return nil, opError("Events", e)
	}
	o, e := normalizeEventOptions(o)
	if e != nil {
		return nil, opError("Events", e)
	}
	if c == nil || c.ctx == nil || c.streams == nil {
		return nil, opError("Events", ErrInvalidHandle)
	}
	c.mu.Lock()
	if c.closed {
		e := c.terminal
		c.mu.Unlock()
		return nil, opError("Events", errors.Join(ErrClosed, e))
	}
	if len(c.streams) >= c.opts.MaxStreams || o.MaxBytes > c.opts.EventBytes-c.eventReserved {
		c.mu.Unlock()
		return nil, opError("Events", ErrResourceLimit)
	}
	s := &EventStream{conn: c, maxBytes: o.MaxBytes, maxCount: o.MaxCount, queue: []Event{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	c.streams[s] = struct{}{}
	c.eventReserved += o.MaxBytes
	c.work.Go(func() {
		select {
		case <-ctx.Done():
			s.finish(ctx.Err())
		case <-s.done:
		case <-c.ctx.Done():
			s.finish(c.closedError())
		}
	})
	c.mu.Unlock()
	return s, nil
}
func (s *EventStream) release() {
	s.releaseOnce.Do(func() {
		if s.conn != nil {
			s.conn.mu.Lock()
			if _, ok := s.conn.streams[s]; ok {
				delete(s.conn.streams, s)
				s.conn.eventReserved -= s.maxBytes
			}
			s.conn.mu.Unlock()
		}
	})
}
func (s *EventStream) finish(err error) {
	if err == nil {
		err = io.EOF
	}
	s.mu.Lock()
	if s.terminal != nil {
		s.mu.Unlock()
		return
	}
	s.terminal = err
	clear(s.queue)
	s.queue = nil
	s.bytes = 0
	s.mu.Unlock()
	s.release()
	close(s.done)
}
func (s *EventStream) push(e Event) {
	n := e.eventBytes()
	s.mu.Lock()
	if s.terminal != nil {
		s.mu.Unlock()
		return
	}
	if len(s.queue) >= s.maxCount || n > s.maxBytes-s.bytes {
		s.mu.Unlock()
		s.finish(ErrEventsLost)
		return
	}
	s.queue = append(s.queue, e.cloneEvent())
	s.bytes += n
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Next permits only one simultaneous reader. Cancellation of this individual
// call does not remove an event or terminate the stream.
func (s *EventStream) Next(ctx context.Context) (Event, error) {
	if s == nil || s.done == nil {
		return nil, ErrInvalidHandle
	}
	if ctx == nil {
		return nil, invalid("nil context")
	}
	if !s.reading.CompareAndSwap(false, true) {
		return nil, ErrConcurrentRead
	}
	defer s.reading.Store(false)
	for {
		s.mu.Lock()
		if s.terminal != nil {
			e := s.terminal
			s.mu.Unlock()
			return nil, e
		}
		if e := ctx.Err(); e != nil {
			s.mu.Unlock()
			return nil, e
		}
		if len(s.queue) > 0 {
			e := s.queue[0]
			s.bytes -= e.eventBytes()
			copy(s.queue, s.queue[1:])
			s.queue[len(s.queue)-1] = nil
			s.queue = s.queue[:len(s.queue)-1]
			s.mu.Unlock()
			return e, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
		case <-s.wake:
		}
	}
}
func (s *EventStream) Close() error {
	if s == nil || s.done == nil {
		return ErrInvalidHandle
	}
	s.finish(io.EOF)
	return nil
}
func (c *Connection) publish(e Event) {
	c.mu.Lock()
	streams := make([]*EventStream, 0, len(c.streams))
	for s := range c.streams {
		streams = append(streams, s)
	}
	c.mu.Unlock()
	for _, s := range streams {
		s.push(e)
	}
}
