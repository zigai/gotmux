package tmux

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"
)

func eventConnection(t *testing.T) *Connection {
	t.Helper()

	_, cancel := context.WithCancelCause(context.Background())
	opts, _ := normalizeControlOptions(ControlOptions{PaneOutput: false, NoEcho: false, ClientFlags: nil, UTF8: UTF8Default, Colors256: false, TerminalFeatures: nil, QueueDepth: 0, QueuedBytes: 0, FrameBytes: 0, EventBytes: 0, MaxStreams: 0})
	//nolint:exhaustruct_v5 // test fixture intentionally initializes mock connection fields
	c := &Connection{cancel: cancel, stopCh: make(chan struct{}), opts: opts, streams: map[*EventStream]struct{}{}, done: make(chan struct{})}

	t.Cleanup(func() { c.stop(nil, true); c.work.Wait() })

	return c
}

func oneEvent() Event {
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

func TestDecodeEventsPauseAndContinue(t *testing.T) {
	// Test %pause
	ev, err := decodeEvent([]byte("%pause %4\n"), 4096)
	if err != nil {
		t.Fatalf("decode %%%%pause failed: %v", err)
	}

	pauseEv, ok := ev.(PanePauseEvent)
	if !ok || pauseEv.PaneID != "%4" {
		t.Fatalf("expected PanePauseEvent for %%4, got %#v", ev)
	}

	if pauseEv.eventBytes() <= 0 {
		t.Errorf("expected positive eventBytes, got %d", pauseEv.eventBytes())
	}

	_ = pauseEv.cloneEvent()

	// Test %continue
	ev, err = decodeEvent([]byte("%continue %7\n"), 4096)
	if err != nil {
		t.Fatalf("decode %%%%continue failed: %v", err)
	}

	contEv, ok := ev.(PaneContinueEvent)
	if !ok || contEv.PaneID != "%7" {
		t.Fatalf("expected PaneContinueEvent for %%7, got %#v", ev)
	}

	if contEv.eventBytes() <= 0 {
		t.Errorf("expected positive eventBytes, got %d", contEv.eventBytes())
	}

	_ = contEv.cloneEvent()
}

func TestDecodeEventsSubscriptionChanged(t *testing.T) {
	// Test %subscription-changed with window index
	ev, err := decodeEvent([]byte("%subscription-changed mysub $1 @2 3 %4 : hello\n"), 4096)
	if err != nil {
		t.Fatalf("decode %%%%subscription-changed failed: %v", err)
	}

	subEv, ok := ev.(SubscriptionEvent)
	if !ok {
		t.Fatalf("expected SubscriptionEvent, got %#v", ev)
	}

	if subEv.Name != "mysub" {
		t.Errorf("expected sub name 'mysub', got %q", subEv.Name)
	}

	if idx, has := subEv.WindowIndex.Get(); !has || idx != 3 {
		t.Errorf("expected WindowIndex 3, got %v (has=%v)", idx, has)
	}

	if string(subEv.Data()) != "hello" {
		t.Errorf("expected Data 'hello', got %q", string(subEv.Data()))
	}

	// Test %subscription-changed without window index
	ev, err = decodeEvent([]byte("%subscription-changed sess_sub $1 - - - : sess_val\n"), 4096)
	if err != nil {
		t.Fatalf("decode session %%%%subscription-changed failed: %v", err)
	}

	subEv2, ok := ev.(SubscriptionEvent)
	if !ok {
		t.Fatalf("expected SubscriptionEvent, got %#v", ev)
	}

	if _, has := subEv2.WindowIndex.Get(); has {
		t.Errorf("expected no WindowIndex for session subscription")
	}

	if string(subEv2.Data()) != "sess_val" {
		t.Errorf("expected Data 'sess_val', got %q", string(subEv2.Data()))
	}
}

func TestDecodeEventCRLF(t *testing.T) {
	// Test CRLF trimming in decodeEvent
	ev, err := decodeEvent([]byte("%session-changed $0 s1\r\n"), 4096)
	if err != nil {
		t.Fatalf("decodeEvent with CRLF failed: %v", err)
	}

	sessEv, ok := ev.(SessionEvent)
	if !ok || sessEv.RawName() != "session-changed" {
		t.Fatalf("expected SessionEvent, got %#v", ev)
	}

	if name, ok := sessEv.Name.Get(); !ok || name != "s1" {
		t.Errorf("expected session name 's1', got %q", name)
	}
}

func TestDecodeConfigErrorAndMessageEvents(t *testing.T) {
	ev, err := decodeEvent([]byte("%config-error bad syntax in conf line 42\n"), 4096)
	if err != nil {
		t.Fatalf("decode config-error failed: %v", err)
	}

	cfgErr, ok := ev.(ConfigErrorEvent)
	if !ok || cfgErr.Error != "bad syntax in conf line 42" {
		t.Fatalf("unexpected ConfigErrorEvent: %+v", ev)
	}

	ev, err = decodeEvent([]byte("%message server reloaded\n"), 4096)
	if err != nil {
		t.Fatalf("decode message failed: %v", err)
	}

	msgEv, ok := ev.(MessageEvent)
	if !ok || msgEv.Message != "server reloaded" {
		t.Fatalf("unexpected MessageEvent: %+v", ev)
	}
}

func TestDecodeClientAndPaneModeEvents(t *testing.T) {
	ev, err := decodeEvent([]byte("%client-flags-changed /dev/pts/1 read-only\n"), 4096)
	if err != nil {
		t.Fatalf("decode client-flags-changed failed: %v", err)
	}

	clientFlagsEv, ok := ev.(ClientFlagsChangedEvent)
	if !ok || clientFlagsEv.ClientName != ClientName("/dev/pts/1") || clientFlagsEv.Flags != "read-only" {
		t.Fatalf("unexpected ClientFlagsChangedEvent: %+v", ev)
	}

	ev, err = decodeEvent([]byte("%pane-mode-changed %5\n"), 4096)
	if err != nil {
		t.Fatalf("decode pane-mode-changed failed: %v", err)
	}

	paneModeEv, ok := ev.(PaneModeChangedEvent)
	if !ok || paneModeEv.PaneID != PaneID("%5") {
		t.Fatalf("unexpected PaneModeChangedEvent: %+v", ev)
	}
}

func TestDecodePasteBufferEvents(t *testing.T) {
	ev, err := decodeEvent([]byte("%paste-buffer-changed buffer1\n"), 4096)
	if err != nil {
		t.Fatalf("decode paste-buffer-changed failed: %v", err)
	}

	bufChangedEv, ok := ev.(PasteBufferChangedEvent)
	if !ok || bufChangedEv.Name != "buffer1" {
		t.Fatalf("unexpected PasteBufferChangedEvent: %+v", ev)
	}

	ev, err = decodeEvent([]byte("%paste-buffer-deleted buffer1\n"), 4096)
	if err != nil {
		t.Fatalf("decode paste-buffer-deleted failed: %v", err)
	}

	bufDelEv, ok := ev.(PasteBufferDeletedEvent)
	if !ok || bufDelEv.Name != "buffer1" {
		t.Fatalf("unexpected PasteBufferDeletedEvent: %+v", ev)
	}
}

func TestDecodeWindowPaneChangedEvent(t *testing.T) {
	ev, err := decodeEvent([]byte("%window-pane-changed @3 %7\n"), 4096)
	if err != nil {
		t.Fatalf("decode window-pane-changed failed: %v", err)
	}

	winPaneEv, ok := ev.(WindowPaneChangedEvent)
	if !ok || winPaneEv.WindowID != WindowID("@3") || winPaneEv.PaneID != PaneID("%7") {
		t.Fatalf("unexpected WindowPaneChangedEvent: %+v", ev)
	}
}

func TestEventStream_BurstSaturation(t *testing.T) {
	c := eventConnection(t)

	// Stream 1: Large capacity stream that should survive high throughput
	s1, err := c.Events(context.Background(), EventOptions{
		MaxBytes: 10 * 1024 * 1024,
		MaxCount: 60000,
		Overflow: FailOnOverflow,
	})
	if err != nil {
		t.Fatalf("failed to create stream 1: %v", err)
	}

	// Stream 2: Small capacity stream that will overflow under burst
	s2, err := c.Events(context.Background(), EventOptions{
		MaxBytes: 2048,
		MaxCount: 5,
		Overflow: FailOnOverflow,
	})
	if err != nil {
		t.Fatalf("failed to create stream 2: %v", err)
	}

	const totalEvents = 10000
	for i := range totalEvents {
		c.publish(PaneOutputEvent{
			eventBase: eventBase{name: "output", received: time.Now()},
			PaneID:    PaneID("%0"),
			Age:       UnavailableValue[time.Duration](),
			data:      []byte(strconv.Itoa(i)),
		})
	}

	// Stream 2 must have failed with ErrEventsLost due to capacity overflow
	_, err2 := s2.Next(context.Background())
	if !errors.Is(err2, ErrEventsLost) {
		t.Fatalf("expected ErrEventsLost on saturated stream 2, got: %v", err2)
	}

	// Stream 1 must have survived and preserved the first event intact
	ev1, err1 := s1.Next(context.Background())
	if err1 != nil {
		t.Fatalf("expected stream 1 to survive burst, got: %v", err1)
	}

	outEv, ok := ev1.(PaneOutputEvent)
	if !ok || string(outEv.Data()) != "0" {
		t.Fatalf("expected first event '0', got %+v", ev1)
	}

	_ = s1.Close()
	_ = s2.Close()
}
