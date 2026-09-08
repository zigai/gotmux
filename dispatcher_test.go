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

	"example.com/tmux/internal/codec"
	"golang.org/x/sync/semaphore"
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
	o, _ := normalizeControlOptions(ControlOptions{QueueDepth: depth, QueuedBytes: 65536})
	rd, wr, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	c := &Connection{original: s, identity: fixtureIdentity(s), opts: o, ctx: ctx, cancel: cancel, streams: map[*EventStream]struct{}{}, count: semaphore.NewWeighted(int64(depth)), bytes: semaphore.NewWeighted(o.QueuedBytes), requests: make(chan *controlRequest, depth), frames: make(chan controlFrame, 1), ready: make(chan error, 1), done: make(chan struct{}), stdin: wr}
	cp := *s
	cp.conn = c
	cp.bound = &c.identity
	cp.lifetime = c
	c.server = &cp
	p := &dispatcherPeer{c: c, writes: make(chan string, 16), release: make(chan struct{})}
	const ready = "TGO-READY:fixture\n"
	c.work.Go(func() { c.dispatch(ready, &guard{identity: c.identity}) })
	c.work.Go(func() {
		defer rd.Close()
		r := bufio.NewReader(rd)
		num := uint64(17)
		send := func(data []byte) bool {
			num += 7
			select {
			case c.frames <- controlFrame{id: frameID{time: 100, number: num, flags: 1}, data: data}:
				return true
			case <-c.ctx.Done():
				return false
			}
		}
		for {
			wire, e := r.ReadString('\n')
			if e != nil {
				return
			}
			end, e := r.ReadString('\n')
			if e != nil {
				return
			}
			select {
			case p.writes <- wire:
			case <-c.ctx.Done():
				return
			}
			words, e := codec.ParseWords(strings.TrimSuffix(end, "\n"))
			if e != nil || len(words) != 3 {
				c.stop(ErrProtocol, false)
				return
			}
			value := "second"
			if strings.Contains(wire, "first") {
				value = "first"
				select {
				case <-p.release:
				case <-c.ctx.Done():
					return
				}
			}
			if !send(codec.EncodeRecord([]string{value})) {
				return
			}
			if !send([]byte(words[2] + "\n")) {
				return
			}
		}
	})
	go func() { c.work.Wait(); close(c.done) }()
	c.frames <- controlFrame{id: frameID{time: 1, number: 42, flags: 0}, data: []byte(guardOK)}
	c.frames <- controlFrame{id: frameID{time: 1, number: 51, flags: 0}, data: []byte(ready)}
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
func peerCall(c *Connection, ctx context.Context, label string) (Result, error) {
	op, e := c.original.begin(ctx)
	if e != nil {
		return Result{}, e
	}
	defer op.close()
	p := recordsPlan(command("display-message", "-p", label))
	n, e := p.size(1 << 20)
	if e != nil {
		return Result{}, e
	}
	return c.run(op, p, n+controlWireOverhead)
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
	go func() { _, e := peerCall(p.c, ctx, "first"); first <- e }()
	waitWrite(t, p)
	cancel()
	e := <-first
	if !errors.Is(e, context.Canceled) || outcomeOf(e).Effect != Unknown {
		t.Fatalf("dispatched cancellation: %v %#v", e, outcomeOf(e))
	}
	if p.c.count.TryAcquire(1) {
		p.c.count.Release(1)
		t.Fatal("draining slot released prematurely")
	}
	second := make(chan controlReply, 1)
	go func() {
		r, e := peerCall(p.c, context.Background(), "second")
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
	rows, e := codec.ParseRecords(reply.result.Stdout, 1)
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
	go func() { _, e := peerCall(p.c, context.Background(), "first"); first <- e }()
	waitWrite(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() { _, e := peerCall(p.c, ctx, "queued"); queued <- e }()
	deadline := time.Now().Add(time.Second)
	for len(p.c.requests) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request not queued")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	e := <-queued
	if !errors.Is(e, context.Canceled) || outcomeOf(e).Effect != NotSent {
		t.Fatalf("queued cancellation: %v %#v", e, outcomeOf(e))
	}
	p.unblock()
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	r, e := peerCall(p.c, context.Background(), "second")
	if e != nil || !strings.Contains(string(r.Stdout), "second") {
		t.Fatal(r, e)
	}
	if len(p.writes) != 1 {
		t.Fatal("canceled queued request was written")
	}
}
func TestDispatcherRawRejectedBeforeWrite(t *testing.T) {
	p := dispatcherFixture(t, 1)
	op, e := p.c.original.begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer op.close()
	r, e := p.c.run(op, plainPlan(command("capture-pane", "-p")), 100)
	if !errors.Is(e, ErrTransportUnsupported) || outcomeOf(e).Effect != NotSent || r.ExitCode != -1 {
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
	go func() { _, e := peerCall(p.c, context.Background(), "first"); result <- e }()
	waitWrite(t, p)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- p.c.Close() }()
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
	_, e := peerCall(p.c, context.Background(), "second")
	if !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestDispatcherFrameFailureFailsPending(t *testing.T) {
	p := dispatcherFixture(t, 1)
	result := make(chan error, 1)
	go func() { _, e := peerCall(p.c, context.Background(), "first"); result <- e }()
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
