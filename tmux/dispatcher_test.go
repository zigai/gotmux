package tmux

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/zigai/gotmux/internal/wire"
)

// This is a deterministic peer model, NOT evidence of tmux compatibility. The
// protocol codec and dispatcher are tested independently and the real suite
// exercises their composition against an actual tmux executable.
type dispatcherPeer struct {
	c           *Connection
	writes      chan string
	release     chan struct{}
	releaseOnce sync.Once
}

func dispatcherFixture(t *testing.T, depth int) *dispatcherPeer {
	t.Helper()
	s := localServer(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	o, _ := normalizeControlOptions(ControlOptions{QueueDepth: depth, QueuedBytes: 65536, PaneOutput: false, NoEcho: false, Flags: nil, UTF8: UTF8Default, Colors256: false, TerminalFeatures: nil, FrameBytes: 0, EventBytes: 0, MaxStreams: 0})

	rd, wr, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}

	//nolint:exhaustruct_v5 // mock Connection test fixture intentionally omits unneeded subprocess and buffer fields
	c := &Connection{original: s, identity: fixtureIdentity(s), opts: o, cancel: cancel, stopCh: make(chan struct{}), streams: map[*EventStream]struct{}{}, count: semaphore.NewWeighted(int64(depth)), bytes: semaphore.NewWeighted(o.QueuedBytes), requests: make(chan *controlRequest, depth), frames: make(chan controlFrame, 1), ready: make(chan error, 1), done: make(chan struct{}), stdin: wr}
	cp := *s
	cp.conn = c
	cp.bound = &c.identity
	cp.lifetime = c
	c.server = &cp
	p := &dispatcherPeer{c: c, writes: make(chan string, 16), release: make(chan struct{}), releaseOnce: sync.Once{}}

	const ready = "TGO-READY:fixture\n"

	c.work.Go(func() { c.dispatch(ctx, ready) })

	c.work.Go(func() { p.mockTmuxLoop(rd, c) })
	go func() { c.work.Wait(); close(c.done) }()

	c.frames <- controlFrame{id: frameID{time: 1, number: 42, flags: 0}, data: []byte(fixtureIdentityRecord(s)), failed: false}

	c.frames <- controlFrame{id: frameID{time: 1, number: 51, flags: 0}, data: []byte(ready), failed: false}

	if e := <-c.ready; e != nil {
		t.Fatal(e)
	}

	t.Cleanup(func() {
		p.releaseOnce.Do(func() { close(p.release) })

		if e := c.Close(); e != nil {
			t.Error(e)
		}
	})

	return p
}
func (p *dispatcherPeer) unblock() { p.releaseOnce.Do(func() { close(p.release) }) }
func sendMockFrame(c *Connection, data []byte, num *uint64) bool {
	*num += 7
	select {
	case c.frames <- controlFrame{id: frameID{time: 100, number: *num, flags: 1}, data: data, failed: false}:
		return true
	case <-c.stopCh:
		return false
	}
}

func (p *dispatcherPeer) mockTmuxLoop(rd *os.File, c *Connection) {
	defer func() { _ = rd.Close() }()

	r := bufio.NewReader(rd)
	num := uint64(17)

	for {
		wireStr, e := r.ReadString('\n')
		if e != nil {
			return
		}

		end, e := r.ReadString('\n')
		if e != nil {
			return
		}

		select {
		case p.writes <- wireStr:
		case <-c.stopCh:
			return
		}

		words, ok := parseEndWords(end)
		if !ok {
			c.stop(ErrProtocol, false)
			return
		}

		data, ok := p.mockResponse(wireStr, c)
		if !ok {
			return
		}

		if !sendMockFrame(c, data, &num) {
			return
		}

		if !sendMockFrame(c, []byte(words[2]+"\n"), &num) {
			return
		}
	}
}

func (p *dispatcherPeer) mockResponse(wireStr string, c *Connection) ([]byte, bool) {
	value := "second"
	if strings.Contains(wireStr, "first") {
		value = "first"

		select {
		case <-p.release:
		case <-c.stopCh:
			return nil, false
		}
	}

	data := wire.EncodeRecord([]string{value})
	if strings.Contains(wireStr, strings.TrimSuffix(guardOK, "\n")) {
		data = append([]byte(guardOK), data...)
	}

	return data, true
}

func parseEndWords(end string) ([]string, bool) {
	words, err := wire.ParseWords(strings.TrimSuffix(end, "\n"))
	if err != nil || len(words) != 3 {
		return nil, false
	}

	return words, true
}

func peerCall(ctx context.Context, c *Connection, label string) (Result, error) {
	opCtx, op, e := c.original.begin(ctx)
	if e != nil {
		return Result{}, e
	}
	defer op.close()

	p := recordsPlan(command("display-message", "-p", label))

	n, e := p.size(1 << 20)
	if e != nil {
		return Result{}, e
	}

	return c.run(opCtx, op, p, n+controlWireOverhead)
}

func waitWrite(t *testing.T, p *dispatcherPeer) string {
	t.Helper()

	select {
	case s := <-p.writes:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("request not dispatched")
		return ""
	}
}

func TestDispatcherCancellationDrainsBeforeNext(t *testing.T) {
	p := dispatcherFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)

	go func() { _, e := peerCall(ctx, p.c, "first"); first <- e }()

	waitWrite(t, p)
	cancel()

	e := <-first
	if !errors.Is(e, context.Canceled) || outcomeOf(e).Effect != EffectUnknown {
		t.Fatalf("dispatched cancellation: %v %#v", e, outcomeOf(e))
	}

	if p.c.count.TryAcquire(1) {
		p.c.count.Release(1)
		t.Fatal("draining slot released prematurely")
	}

	second := make(chan controlReply, 1)

	go func() {
		r, e := peerCall(context.Background(), p.c, "second")
		second <- controlReply{result: r, err: e}
	}()

	select {
	case <-p.writes:
		t.Fatal("next request written before drain")
	case <-time.After(20 * time.Millisecond):
	}

	p.unblock()
	waitWrite(t, p)

	reply := <-second

	rows, e := wire.ParseRecords(reply.result.Stdout, 1)
	if reply.err != nil || e != nil || len(rows) != 1 || rows[0][0] != "second" {
		t.Fatalf("late reply misattribution: %#v %v %v", rows, e, reply.err)
	}

	if !p.c.count.TryAcquire(1) {
		t.Fatal("slot leak")
	}

	p.c.count.Release(1)

	if !p.c.bytes.TryAcquire(p.c.opts.QueuedBytes) {
		t.Fatal("byte reservation leak")
	}

	p.c.bytes.Release(p.c.opts.QueuedBytes)
}

func TestDispatcherQueuedCancellationNotSent(t *testing.T) {
	p := dispatcherFixture(t, 2)
	first := make(chan error, 1)

	go func() { _, e := peerCall(context.Background(), p.c, "first"); first <- e }()

	waitWrite(t, p)

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)

	go func() { _, e := peerCall(ctx, p.c, "queued"); queued <- e }()

	deadline := time.Now().Add(time.Second)
	for len(p.c.requests) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request not queued")
		}

		time.Sleep(time.Millisecond)
	}

	cancel()

	e := <-queued
	if !errors.Is(e, context.Canceled) || outcomeOf(e).Effect != EffectNotSent {
		t.Fatalf("queued cancellation: %v %#v", e, outcomeOf(e))
	}

	p.unblock()

	if e := <-first; e != nil {
		t.Fatal(e)
	}

	r, e := peerCall(context.Background(), p.c, "second")
	if e != nil || !strings.Contains(string(r.Stdout), "second") {
		t.Fatal(r, e)
	}

	if len(p.writes) != 1 {
		t.Fatal("canceled queued request was written")
	}
}

func TestDispatcherAdmissionDeadlineIsCallerTimeout(t *testing.T) {
	peer := dispatcherFixture(t, 1)
	first := make(chan error, 1)

	go func() { _, err := peerCall(context.Background(), peer.c, "first"); first <- err }()

	waitWrite(t, peer)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := peerCall(ctx, peer.c, "second")

	commandErr, ok := errors.AsType[*CommandError](err)
	if !ok || !errors.Is(err, context.DeadlineExceeded) || commandErr.Timeout != TimeoutSourceCaller || commandErr.Outcome.Effect != EffectNotSent {
		t.Errorf("capacity admission deadline: %v, command error %#v", err, commandErr)
	}

	peer.unblock()

	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestDispatcherCanceledLifetimeRejectsAdmissionAfterDrain(t *testing.T) {
	server := localServer(t)

	var options ControlOptions

	options.QueueDepth = 1
	options.QueuedBytes = 4096

	opts, err := normalizeControlOptions(options)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)

	connection := newConnection(server, opts, cancel)
	finished := make(chan struct{})

	go func() { connection.dispatch(ctx, "TGO-READY:fixture\n"); close(finished) }()

	connection.frames <- controlFrame{id: frameID{time: 1, number: 42, flags: 0}, data: []byte(fixtureIdentityRecord(server)), failed: false}

	connection.frames <- controlFrame{id: frameID{time: 1, number: 51, flags: 0}, data: []byte("TGO-READY:fixture\n"), failed: false}

	if err := <-connection.ready; err != nil {
		t.Fatal(err)
	}

	cancel(context.Canceled)

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not exit")
	}

	if err := connection.acquireCapacity(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	var request controlRequest

	request.bytes = 1
	request.result = make(chan controlReply, 1)

	err = connection.enqueueRequest(&request)
	if !errors.Is(err, ErrClosed) {
		connection.drainRequests()
		t.Fatalf("admission after dispatcher exit = %v, want closed", err)
	}

	if !connection.count.TryAcquire(1) || !connection.bytes.TryAcquire(opts.QueuedBytes) {
		t.Fatal("admission after dispatcher exit retained permits")
	}
}

func TestDispatcherEnqueueShutdownReleasesCapacity(t *testing.T) {
	server := localServer(t)

	var options ControlOptions

	options.QueueDepth = 1
	options.QueuedBytes = 4096

	opts, err := normalizeControlOptions(options)
	if err != nil {
		t.Fatal(err)
	}

	for iteration := range 20000 {
		_, cancel := context.WithCancelCause(context.Background())

		connection := newConnection(server, opts, cancel)
		if err := connection.acquireCapacity(context.Background(), 1); err != nil {
			t.Fatal(err)
		}

		var request controlRequest

		request.bytes = 1
		request.result = make(chan controlReply, 1)
		start := make(chan struct{})
		enqueued := make(chan error, 1)
		stopped := make(chan struct{})

		go func() { <-start; enqueued <- connection.enqueueRequest(&request) }()
		go func() { <-start; connection.stop(nil, true); connection.drainRequests(); close(stopped) }()

		close(start)
		<-enqueued
		<-stopped
		cancel(nil)

		if len(connection.requests) != 0 {
			connection.drainRequests()
			t.Fatalf("iteration %d: request retained after shutdown drain", iteration)
		}

		if !connection.count.TryAcquire(1) || !connection.bytes.TryAcquire(opts.QueuedBytes) {
			t.Fatalf("iteration %d: admission permits retained after shutdown", iteration)
		}
	}
}

func TestDispatcherRawRejectedBeforeWrite(t *testing.T) {
	p := dispatcherFixture(t, 1)

	ctx := context.Background()

	opCtx, op, e := p.c.original.begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer op.close()

	r, e := p.c.run(opCtx, op, plainPlan(command("capture-pane", "-p")), 100)
	if !errors.Is(e, ErrTransportUnsupported) || outcomeOf(e).Effect != EffectNotSent || r.ExitCode != -1 {
		t.Fatal(r, e)
	}

	select {
	case <-p.writes:
		t.Fatal("unsupported request written")
	default:
	}
}

func TestDispatcherCloseConcurrentAndWait(t *testing.T) {
	p := dispatcherFixture(t, 2)
	result := make(chan error, 1)

	go func() { _, e := peerCall(context.Background(), p.c, "first"); result <- e }()

	waitWrite(t, p)

	var wg sync.WaitGroup

	errs := make(chan error, 10)

	for range 10 {
		wg.Go(func() { ; errs <- p.c.Close() })
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}

	if e := <-result; !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}

	if e := p.c.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}

	_, e := peerCall(context.Background(), p.c, "second")
	if !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}

func TestDispatcherFrameFailureFailsPending(t *testing.T) {
	p := dispatcherFixture(t, 1)
	result := make(chan error, 1)

	go func() { _, e := peerCall(context.Background(), p.c, "first"); result <- e }()

	waitWrite(t, p)
	p.c.stop(io.ErrUnexpectedEOF, false)

	e := <-result
	if !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatal(e)
	}

	if e := p.c.Wait(context.Background()); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatal(e)
	}

	p.c.mu.Lock()
	p.c.normal = true
	p.c.mu.Unlock() // test cleanup already observed the retained failure
}
