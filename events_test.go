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
	ctx, cancel := context.WithCancelCause(context.Background())
	opts, _ := normalizeControlOptions(ControlOptions{})
	c := &Connection{ctx: ctx, cancel: cancel, opts: opts, streams: map[*EventStream]struct{}{}, done: make(chan struct{})}
	t.Cleanup(func() { c.stop(nil, true); c.work.Wait() })
	return c
}
func oneEvent() Event {
	return PaneOutputEvent{eventBase: eventBase{name: "output"}, PaneID: "%1", data: []byte("abc")}
}
func TestEventOverflowIsTerminalWithoutNextEvent(t *testing.T) {
	c := eventConnection(t)
	s, e := c.Events(context.Background(), EventOptions{MaxCount: 1, MaxBytes: 1024})
	if e != nil {
		t.Fatal(e)
	}
	s.push(oneEvent())
	s.push(oneEvent())
	for i := 0; i < 3; i++ {
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
	s, e := c.Events(context.Background(), EventOptions{})
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
	if e != nil || string(ev.(PaneOutputEvent).Data()) != "abc" {
		t.Fatal(ev, e)
	}
	s.Close()
	if _, e := s.Next(context.Background()); e != io.EOF {
		t.Fatal(e)
	}
}
func TestEventReservationCeiling(t *testing.T) {
	c := eventConnection(t)
	c.opts.EventBytes = 1024
	c.opts.MaxStreams = 2
	s, e := c.Events(context.Background(), EventOptions{MaxBytes: 1024})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := c.Events(context.Background(), EventOptions{MaxBytes: 1}); !errors.Is(e, ErrResourceLimit) {
		t.Fatal(e)
	}
	s.Close()
	if _, e := c.Events(context.Background(), EventOptions{MaxBytes: 1024}); e != nil {
		t.Fatal(e)
	}
}
func TestEventContextErrorPersists(t *testing.T) {
	c := eventConnection(t)
	ctx, cancel := context.WithCancel(context.Background())
	s, e := c.Events(ctx, EventOptions{})
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("stream did not close")
	}
	s.Close()
	for i := 0; i < 2; i++ {
		if _, e := s.Next(context.Background()); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
}
func TestEventSingleReader(t *testing.T) {
	c := eventConnection(t)
	s, e := c.Events(context.Background(), EventOptions{})
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
	slow, e := c.Events(context.Background(), EventOptions{MaxCount: 1})
	if e != nil {
		t.Fatal(e)
	}
	fast, e := c.Events(context.Background(), EventOptions{MaxCount: 10})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
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
	s, e := c.Events(context.Background(), EventOptions{})
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.push(oneEvent())
			}
			s.Close()
		}()
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
	if _, e := c.Events(context.Background(), EventOptions{}); !errors.Is(e, ErrInvalidHandle) {
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
