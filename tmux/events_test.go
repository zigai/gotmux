package tmux

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func eventConnection(t *testing.T) *Connection {
	t.Helper()

	_, cancel := context.WithCancelCause(context.Background())
	opts, _ := normalizeControlOptions(ControlOptions{PaneOutput: false, QueueDepth: 0, QueuedBytes: 0, FrameBytes: 0, EventBytes: 0, MaxStreams: 0})
	//nolint:exhaustruct_v5 // test fixture intentionally initializes mock connection fields
	c := &Connection{cancel: cancel, stopCh: make(chan struct{}), opts: opts, streams: map[*EventStream]struct{}{}, done: make(chan struct{})}

	t.Cleanup(func() { c.stop(nil, true); c.work.Wait() })

	return c
}

func oneEvent() Event {
	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	return PaneOutputEvent{eventBase: eventBase{name: "output", received: time.Now()}, PaneID: "%1", Age: UnavailableValue[time.Duration](), data: []byte("abc")}
}

func testEventOptions(bytes int64, count int) EventOptions {
	return EventOptions{
		MaxBytes: bytes,
		MaxCount: count,
		Overflow: FailOnOverflow,
	}
}

func TestEventOverflowIsTerminalWithoutNextEvent(t *testing.T) {
	c := eventConnection(t)

	s, e := c.Events(context.Background(), testEventOptions(1024, 1))
	if e != nil {
		t.Fatal(e)
	}

	s.push(oneEvent())
	s.push(oneEvent())

	for range 3 {
		if _, e := s.Next(context.Background()); !errors.Is(e, ErrEventsLost) {
			t.Fatal(e)
		}
	}

	if s.bytes != 0 || len(s.queue) != 0 || c.eventReserved != 0 {
		t.Fatal("reservation leak")
	}
}

func TestCanceledNextDoesNotConsume(t *testing.T) {
	c := eventConnection(t)

	s, e := c.Events(context.Background(), testEventOptions(0, 0))
	if e != nil {
		t.Fatal(e)
	}

	s.push(oneEvent())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, e := s.Next(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}

	ev, e := s.Next(context.Background())

	poe, ok := ev.(PaneOutputEvent)
	if e != nil || !ok || string(poe.Data()) != "abc" {
		t.Fatal(ev, e)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, e := s.Next(context.Background()); !errors.Is(e, io.EOF) {
		t.Fatal(e)
	}
}

func TestEventReservationCeiling(t *testing.T) {
	c := eventConnection(t)
	c.opts.EventBytes = 1024
	c.opts.MaxStreams = 2

	s, e := c.Events(context.Background(), testEventOptions(1024, 0))
	if e != nil {
		t.Fatal(e)
	}

	if _, e := c.Events(context.Background(), testEventOptions(1, 0)); !errors.Is(e, ErrResourceLimit) {
		t.Fatal(e)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, e := c.Events(context.Background(), testEventOptions(1024, 0)); e != nil {
		t.Fatal(e)
	}
}

func TestEventContextErrorPersists(t *testing.T) {
	c := eventConnection(t)
	ctx, cancel := context.WithCancel(context.Background())

	s, e := c.Events(ctx, testEventOptions(0, 0))
	if e != nil {
		t.Fatal(e)
	}

	cancel()

	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("stream did not close")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if _, e := s.Next(context.Background()); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
}

func TestEventSingleReader(t *testing.T) {
	c := eventConnection(t)

	s, e := c.Events(context.Background(), testEventOptions(0, 0))
	if e != nil {
		t.Fatal(e)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() { _, e := s.Next(ctx); done <- e }()

	deadline := time.Now().Add(time.Second)
	for !s.reading.Load() {
		if time.Now().After(deadline) {
			t.Fatal("reader did not enter")
		}

		time.Sleep(time.Millisecond)
	}

	if _, e := s.Next(context.Background()); !errors.Is(e, ErrConcurrentRead) {
		t.Fatal(e)
	}

	cancel()

	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestSlowSubscriberDoesNotBreakAnother(t *testing.T) {
	c := eventConnection(t)

	slow, e := c.Events(context.Background(), testEventOptions(0, 1))
	if e != nil {
		t.Fatal(e)
	}

	fast, e := c.Events(context.Background(), testEventOptions(0, 10))
	if e != nil {
		t.Fatal(e)
	}

	for range 3 {
		c.publish(oneEvent())

		if _, e := fast.Next(context.Background()); e != nil {
			t.Fatal(e)
		}
	}

	if _, e := slow.Next(context.Background()); !errors.Is(e, ErrEventsLost) {
		t.Fatal(e)
	}

	if c.closed {
		t.Fatal("subscriber closed connection")
	}
}

func TestEventConcurrentClosure(t *testing.T) {
	c := eventConnection(t)

	s, e := c.Events(context.Background(), testEventOptions(0, 0))
	if e != nil {
		t.Fatal(e)
	}

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 100 {
				s.push(oneEvent())
			}

			_ = s.Close()
		})
	}

	wg.Wait()

	if c.eventReserved != 0 {
		t.Fatal("reservation not released")
	}
}

func TestZeroResourceValues(t *testing.T) {
	var c Connection
	if e := c.Close(); !errors.Is(e, ErrInvalidHandle) {
		t.Fatal(e)
	}

	if _, e := c.Events(context.Background(), testEventOptions(0, 0)); !errors.Is(e, ErrInvalidHandle) {
		t.Fatal(e)
	}

	var s EventStream
	if e := s.Close(); !errors.Is(e, ErrInvalidHandle) {
		t.Fatal(e)
	}

	if _, e := s.Next(context.Background()); !errors.Is(e, ErrInvalidHandle) {
		t.Fatal(e)
	}
}
