package tmux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

type controlWireFixture struct {
	c        *Connection
	stdinRd  *os.File
	stdoutWr *os.File
	writes   chan string
	done     chan struct{}
}

func newControlWireFixture(t *testing.T, depth int) *controlWireFixture {
	t.Helper()
	s := localServer(t)
	ctx, cancel := context.WithCancelCause(context.Background())

	o, err := normalizeControlOptions(ControlOptions{
		QueueDepth:  depth,
		QueuedBytes: 65536,
		PaneOutput:  false,
		FrameBytes:  0,
		EventBytes:  0,
		MaxStreams:  0,
	})
	if err != nil {
		t.Fatal(err)
	}

	inRd, inWr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	outRd, outWr, err := os.Pipe()
	if err != nil {
		_ = inRd.Close()
		_ = inWr.Close()

		t.Fatal(err)
	}

	//nolint:exhaustruct_v5 // mock Connection test fixture intentionally omits unneeded subprocess and buffer fields
	c := &Connection{
		original:      s,
		identity:      fixtureIdentity(s),
		opts:          o,
		cancel:        cancel,
		stopCh:        make(chan struct{}),
		mu:            sync.Mutex{},
		closed:        false,
		terminal:      nil,
		normal:        false,
		streams:       map[*EventStream]struct{}{},
		eventReserved: 0,
		count:         semaphore.NewWeighted(int64(depth)),
		bytes:         semaphore.NewWeighted(o.QueuedBytes),
		requests:      make(chan *controlRequest, depth),
		frames:        make(chan controlFrame, 1),
		ready:         make(chan error, 1),
		done:          make(chan struct{}),
		work:          sync.WaitGroup{},
		cmd:           nil,
		stdin:         inWr,
		stdout:        outRd,
		stderr:        nil,
		diagnostics:   nil,
		stopOnce:      sync.Once{},
	}
	cp := *s
	cp.conn = c
	cp.bound = &c.identity
	cp.lifetime = c
	c.server = &cp

	f := &controlWireFixture{
		c:        c,
		stdinRd:  inRd,
		stdoutWr: outWr,
		writes:   make(chan string, 16),
		done:     make(chan struct{}),
	}

	const ready = "TGO-READY:fixture\n"

	c.work.Go(c.reader)
	c.work.Go(func() { c.dispatch(ctx, ready, newGuard(c.identity)) })

	// Monitor stdin writes from connection
	go func() {
		defer close(f.done)

		r := bufio.NewReader(inRd)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}

			select {
			case f.writes <- line:
			case <-c.stopCh:
				return
			}
		}
	}()

	go func() {
		c.work.Wait()
		close(c.done)
	}()

	// Send handshake through stdout pipe so reader processes it
	handshake := fmt.Sprintf("%%begin 100 1 0\n%s%%end 100 1 0\n%%begin 100 2 0\n%s%%end 100 2 0\n", guardOK, ready)
	if _, err := io.WriteString(outWr, handshake); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-c.ready:
		if err != nil {
			t.Fatalf("startup handshake failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handshake timeout")
	}

	t.Cleanup(func() {
		_ = outWr.Close()
		_ = inRd.Close()
		_ = c.Close()
	})

	return f
}

func (f *controlWireFixture) waitWrite(t *testing.T) string {
	t.Helper()

	select {
	case s := <-f.writes:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stdin write from Connection")
		return ""
	}
}

func (f *controlWireFixture) assertCleanup(t *testing.T) {
	t.Helper()

	c := f.c

	// Verify c.Close() completes within 500ms without deadlocking.
	closeDone := make(chan error, 1)
	start := time.Now()

	go func() {
		closeDone <- c.Close()
	}()

	select {
	case err := <-closeDone:
		dur := time.Since(start)
		if dur > 500*time.Millisecond {
			t.Errorf("c.Close() took %v, exceeded 500ms limit", dur)
		}

		if errors.Is(err, ErrShutdownIncomplete) {
			t.Errorf("c.Close() returned ErrShutdownIncomplete: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("c.Close() deadlocked")
	}

	// Verify background goroutines in c.work finish cleanly
	select {
	case <-c.done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("background goroutines in c.work did not finish cleanly")
	}

	// Verify semaphore permits (c.count and c.bytes) are properly released / accounted for
	if !c.count.TryAcquire(int64(c.opts.QueueDepth)) {
		t.Errorf("semaphore permit leak: could not acquire all %d count permits", c.opts.QueueDepth)
	} else {
		c.count.Release(int64(c.opts.QueueDepth))
	}

	if !c.bytes.TryAcquire(c.opts.QueuedBytes) {
		t.Errorf("semaphore permit leak: could not acquire all %d byte permits", c.opts.QueuedBytes)
	} else {
		c.bytes.Release(c.opts.QueuedBytes)
	}
}

// 1. Truncated control frame %begin 1 4 1\n followed by immediate EOF.
// Verify request fails with an error wrapping ErrProtocol and Effect: Unknown.
func TestControlWireFault_TruncatedFrameEOF(t *testing.T) {
	f := newControlWireFixture(t, 1)

	errCh := make(chan error, 1)

	go func() {
		_, err := peerCall(f.c, context.Background(), "req1")
		errCh <- err
	}()

	f.waitWrite(t)

	// Inject truncated control frame followed by immediate EOF
	if _, err := io.WriteString(f.stdoutWr, "%begin 1 4 1\n"); err != nil {
		t.Fatal(err)
	}

	_ = f.stdoutWr.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected request to fail, got nil error")
		}

		if !errors.Is(err, ErrProtocol) {
			t.Errorf("expected error wrapping ErrProtocol, got %v", err)
		}

		if eff := outcomeOf(err).Effect; eff != Unknown {
			t.Errorf("expected Effect Unknown, got %v", eff)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request failure")
	}

	f.assertCleanup(t)
}

// 2. Premature pipe closure / broken pipe while multiple requests are queued.
// The in-flight request fails with Effect: Unknown; the queued requests fail with Effect: NotSent.
func TestControlWireFault_PrematurePipeClosureQueuedRequests(t *testing.T) {
	f := newControlWireFixture(t, 3)
	inflightErr := make(chan error, 1)

	go func() {
		_, err := peerCall(f.c, context.Background(), "inflight")
		inflightErr <- err
	}()

	f.waitWrite(t)

	queued1Err := make(chan error, 1)
	queued2Err := make(chan error, 1)

	go func() {
		_, err := peerCall(f.c, context.Background(), "queued1")
		queued1Err <- err
	}()
	go func() {
		_, err := peerCall(f.c, context.Background(), "queued2")
		queued2Err <- err
	}()

	// Wait for requests to be queued
	deadline := time.Now().Add(time.Second)
	for len(f.c.requests) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for requests to queue")
		}

		time.Sleep(time.Millisecond)
	}

	// Close pipe prematurely
	_ = f.stdoutWr.Close()

	select {
	case err := <-inflightErr:
		if err == nil {
			t.Fatal("expected inflight request to fail, got nil")
		}

		if eff := outcomeOf(err).Effect; eff != Unknown {
			t.Errorf("expected inflight Effect Unknown, got %v", eff)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inflight request error")
	}

	for i, ch := range []chan error{queued1Err, queued2Err} {
		select {
		case err := <-ch:
			if err == nil {
				t.Fatalf("expected queued request %d to fail, got nil", i+1)
			}

			if eff := outcomeOf(err).Effect; eff != NotSent {
				t.Errorf("expected queued request %d Effect NotSent, got %v", i+1, eff)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for queued request %d error", i+1)
		}
	}

	f.assertCleanup(t)
}

// 3. Unsolicited %exit notification from tmux.
// Verify connection marks itself closed, and subsequent operations return ErrClosed.
func TestControlWireFault_UnsolicitedExit(t *testing.T) {
	f := newControlWireFixture(t, 2)

	// Send unsolicited %exit notification
	if _, err := io.WriteString(f.stdoutWr, "%exit\n"); err != nil {
		t.Fatal(err)
	}

	// Verify connection marks itself closed
	select {
	case <-f.c.stopCh:
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not close stopCh after exit notification")
	}

	if err := f.c.closedError(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected closedError to wrap ErrClosed, got %v", err)
	}

	if err := f.c.Wait(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("expected Wait to return ErrClosed, got %v", err)
	}

	// Subsequent operations must return ErrClosed
	_, err := peerCall(f.c, context.Background(), "subsequent")
	if !errors.Is(err, ErrClosed) {
		t.Errorf("expected subsequent operation to return ErrClosed, got %v", err)
	}

	if eff := outcomeOf(err).Effect; eff != NotSent {
		t.Errorf("expected subsequent operation Effect NotSent, got %v", eff)
	}

	f.assertCleanup(t)
}

// 4. Wire data exceeding Limits.OutputBytes (e.g. 10MB of garbage).
// Verify reader stops with ErrOutputLimit.
func TestControlWireFault_OutputLimitExceeded(t *testing.T) {
	f := newControlWireFixture(t, 2)

	garbageDone := make(chan struct{})
	go func() {
		defer close(garbageDone)

		chunk := bytes.Repeat([]byte("A"), 64*1024)
		total := 0
		// Stream up to 10MB of garbage
		for total < 10<<20 {
			_, err := f.stdoutWr.Write(chunk)
			if err != nil {
				return
			}

			total += len(chunk)
		}
	}()

	// Verify reader stops with ErrOutputLimit
	select {
	case <-f.c.stopCh:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not stop after exceeding OutputBytes limit")
	}

	<-garbageDone

	if err := f.c.closedError(); !errors.Is(err, ErrOutputLimit) {
		t.Errorf("expected closedError to wrap ErrOutputLimit, got %v", err)
	}

	if err := f.c.Wait(context.Background()); !errors.Is(err, ErrOutputLimit) {
		t.Errorf("expected Wait to return ErrOutputLimit, got %v", err)
	}

	f.assertCleanup(t)
}
