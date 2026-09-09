package tmux

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
)

const (
	defaultMaxEventBytes = 4 << 20
	defaultMaxEventCount = 256
)

// OverflowPolicy controls how an [EventStream] handles buffer overflow when events
// arrive faster than the consumer drains them.
const (
	// FailOnOverflow terminates the stream with [ErrEventsLost] when queue limits are breached.
	// Queued events are purged and memory reservations are immediately released.
	FailOnOverflow OverflowPolicy = iota
)

// OverflowPolicy defines the action taken when an event stream's buffer capacity is exceeded.
type OverflowPolicy uint8

// EventOptions configures queue capacity and overflow behavior for an [EventStream].
type EventOptions struct {
	// MaxBytes is the maximum memory in bytes reserved for this stream's queue.
	// This amount is reserved upfront from the connection's [ControlOptions.EventBytes] budget.
	MaxBytes int64

	// MaxCount bounds the maximum number of queued events.
	MaxCount int

	// Overflow determines the behavior when MaxBytes or MaxCount is exceeded.
	Overflow OverflowPolicy
}

// EventStream delivers an asynchronous stream of tmux control notifications.
//
// Concurrency constraint:
// An EventStream supports only ONE concurrent reader in [EventStream.Next]. Multiple goroutines
// attempting concurrent Next calls receive [ErrConcurrentRead].
//
// Cancellation safety:
// Canceling a context passed to Next does NOT discard queued events or terminate the stream.
// Queued events remain buffered for the next Next call.
type EventStream struct {
	conn        *Connection
	mu          sync.Mutex
	queue       []Event
	bytes       int64
	maxBytes    int64
	maxCount    int
	done        chan struct{}
	wake        chan struct{}
	terminal    error
	releaseOnce sync.Once
	reading     atomic.Bool
}

// Events creates a new [EventStream] with upfront reserved byte capacity.
// Its context owns the stream's lifetime; canceling ctx closes the stream with ctx.Err().
// Closing an individual stream releases its reservation and never affects other streams or the connection.
func (c *Connection) Events(ctx context.Context, o EventOptions) (*EventStream, error) {
	if ctx == nil {
		return nil, opError("Events", invalid("nil context"))
	}

	if err := ctx.Err(); err != nil {
		return nil, opError("Events", err)
	}

	o, err := normalizeEventOptions(o)
	if err != nil {
		return nil, opError("Events", err)
	}

	if c == nil || c.done == nil || c.streams == nil {
		return nil, opError("Events", ErrInvalidHandle)
	}

	c.mu.Lock()
	if c.closed {
		err := c.terminal
		c.mu.Unlock()

		return nil, opError("Events", errors.Join(ErrClosed, err))
	}

	if err := c.checkStreamCapacity(o.MaxBytes); err != nil {
		c.mu.Unlock()
		return nil, opError("Events", err)
	}

	s := newEventStream(c, o.MaxBytes, o.MaxCount)
	c.streams[s] = struct{}{}
	c.eventReserved += o.MaxBytes
	c.work.Go(func() {
		select {
		case <-ctx.Done():
			s.finish(ctx.Err())
		case <-s.done:
		case <-c.stopCh:
			s.finish(c.closedError())
		}
	})
	c.mu.Unlock()

	return s, nil
}

// Next blocks until the next [Event] is available, the stream closes, or ctx expires.
//
// Only one reader may call Next at a time. If ctx expires, Next returns ctx.Err()
// while keeping unread events queued safely for subsequent calls.
// Returns [io.EOF] when the stream is closed via [EventStream.Close].
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
			err := s.terminal
			s.mu.Unlock()

			return nil, err
		}

		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, err //nolint:wrapcheck // context cancellation is intentionally returned unwrapped
		}

		if len(s.queue) > 0 {
			event := s.queue[0]
			s.bytes -= event.eventBytes()
			copy(s.queue, s.queue[1:])
			s.queue[len(s.queue)-1] = nil
			s.queue = s.queue[:len(s.queue)-1]
			s.mu.Unlock()

			return event, nil
		}
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err() //nolint:wrapcheck // context cancellation is intentionally returned unwrapped
		case <-s.done:
		case <-s.wake:
		}
	}
}

// Close terminates this event stream and releases its byte capacity reservation.
// Future calls to [EventStream.Next] return [io.EOF]. Does not close the underlying [Connection].
func (s *EventStream) Close() error {
	if s == nil || s.done == nil {
		return ErrInvalidHandle
	}

	s.finish(io.EOF)

	return nil
}

func (c *Connection) checkStreamCapacity(maxBytes int64) error {
	if len(c.streams) >= c.opts.MaxStreams || maxBytes > c.opts.EventBytes-c.eventReserved {
		return ErrResourceLimit
	}

	return nil
}

func newEventStream(conn *Connection, maxBytes int64, maxCount int) *EventStream {
	return &EventStream{
		conn:        conn,
		mu:          sync.Mutex{},
		queue:       nil,
		bytes:       0,
		maxBytes:    maxBytes,
		maxCount:    maxCount,
		done:        make(chan struct{}),
		wake:        make(chan struct{}, 1),
		terminal:    nil,
		releaseOnce: sync.Once{},
		reading:     atomic.Bool{},
	}
}

func normalizeEventOptions(o EventOptions) (EventOptions, error) {
	if o.MaxBytes < 0 || o.MaxCount < 0 || o.Overflow != FailOnOverflow {
		return o, invalid("event options")
	}

	if o.MaxBytes == 0 {
		o.MaxBytes = defaultMaxEventBytes
	}

	if o.MaxCount == 0 {
		o.MaxCount = defaultMaxEventCount
	}

	if o.MaxBytes > 1<<40 || o.MaxCount > 1<<20 {
		return o, invalid("event limits")
	}

	return o, nil
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
