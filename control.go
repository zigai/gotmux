package tmux

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	process "example.com/tmux/internal/exec"
	"example.com/tmux/internal/lifecycle"
	"golang.org/x/sync/semaphore"
)

type ControlOptions struct {
	PaneOutput  bool
	QueueDepth  int
	QueuedBytes int64
	FrameBytes  int64
	EventBytes  int64
	MaxStreams  int
}

func normalizeControlOptions(o ControlOptions) (ControlOptions, error) {
	if o.QueueDepth < 0 || o.QueuedBytes < 0 || o.FrameBytes < 0 || o.EventBytes < 0 || o.MaxStreams < 0 {
		return o, invalid("control limits")
	}
	if o.QueueDepth == 0 {
		o.QueueDepth = 64
	}
	if o.QueuedBytes == 0 {
		o.QueuedBytes = 8 << 20
	}
	if o.FrameBytes == 0 {
		o.FrameBytes = 4 << 20
	}
	if o.EventBytes == 0 {
		o.EventBytes = 32 << 20
	}
	if o.MaxStreams == 0 {
		o.MaxStreams = 8
	}
	if o.QueueDepth > 1<<20 || o.QueuedBytes > 1<<40 || o.FrameBytes > 1<<40 || o.EventBytes > 1<<40 || o.MaxStreams > 1<<16 {
		return o, invalid("control limits")
	}
	return o, nil
}

const controlWireOverhead int64 = 128

var connectionGeneration atomic.Uint64

type controlRequest struct {
	ctx    context.Context
	plan   plan
	bytes  int64
	stdout int64
	stderr int64
	result chan controlReply
	state  atomic.Uint32
}
type controlReply struct {
	result Result
	err    error
}
type Connection struct {
	server        *Server
	original      *Server
	identity      ServerIdentity
	generation    uint64
	opts          ControlOptions
	ctx           context.Context
	cancel        context.CancelCauseFunc
	mu            sync.Mutex
	closed        bool
	terminal      error
	normal        bool
	streams       map[*EventStream]struct{}
	eventReserved int64
	count         *semaphore.Weighted
	bytes         *semaphore.Weighted
	requests      chan *controlRequest
	frames        chan controlFrame
	ready         chan error
	done          chan struct{}
	work          lifecycle.Group
	cmd           *exec.Cmd
	stdin         *os.File
	stdout        *os.File
	stderr        *os.File
	diagnostics   *process.Buffer
	stopOnce      sync.Once
}

func token(prefix string) (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return prefix + hex.EncodeToString(b[:]) + "\n", nil
}

// OpenControl starts one owned control client attached to session. It does not
// create a scratch session, detach any other client, or reconnect. ctx owns the
// entire lifetime. The original Server continues to use subprocess execution.
func (s *Server) OpenControl(ctx context.Context, session Session, o ControlOptions) (*Connection, error) {
	if s == nil || s.runner == nil {
		return nil, opError("OpenControl", ErrInvalidHandle)
	}
	if ctx == nil {
		return nil, opError("OpenControl", invalid("nil context"))
	}
	if e := ctx.Err(); e != nil {
		return nil, opError("OpenControl", e)
	}
	if e := session.h.check(); e != nil {
		return nil, opError("OpenControl", e)
	}
	if s.conn != nil || session.h.origin.Generation != 0 || session.h.origin.Endpoint != s.endpoint {
		return nil, opError("OpenControl", ErrInvalidHandle)
	}
	o, e := normalizeControlOptions(o)
	if e != nil {
		return nil, opError("OpenControl", e)
	}
	nonce, e := token("TGO-READY:")
	if e != nil {
		return nil, opError("OpenControl", e)
	}
	flags := "ignore-size"
	if !o.PaneOutput {
		flags += ",no-output"
	}
	p := session.h.guard().wrap(emptyPlan(command("attach-session", "-t", session.h.id, "-f", flags)))
	p.nodes = append(p.nodes, markerNode(nonce))
	size, e := p.size(s.config.Limits.InputBytes)
	if e != nil {
		return nil, opError("OpenControl", e)
	}
	_ = size
	args, e := p.argv()
	if e != nil {
		return nil, opError("OpenControl", e)
	}
	args = append(append(s.baseArgs(false), "-C"), args...)
	cctx, cancel := context.WithCancelCause(ctx)
	gen := connectionGeneration.Add(1)
	id := session.h.origin
	id.Generation = gen
	c := &Connection{original: s, identity: id, generation: gen, opts: o, ctx: cctx, cancel: cancel, streams: map[*EventStream]struct{}{}, count: semaphore.NewWeighted(int64(o.QueueDepth)), bytes: semaphore.NewWeighted(o.QueuedBytes), requests: make(chan *controlRequest, o.QueueDepth), frames: make(chan controlFrame, 1), ready: make(chan error, 1), done: make(chan struct{})}
	server := *s
	server.conn = c
	server.bound = &c.identity
	server.lifetime = c
	c.server = &server
	c.cmd = s.runner.Command(cctx, args)
	var inChild, outChild, errChild *os.File
	cleanup := func() {
		for _, f := range []*os.File{c.stdin, c.stdout, c.stderr, inChild, outChild, errChild} {
			if f != nil {
				_ = f.Close()
			}
		}
		cancel(ErrClosed)
	}
	inChild, c.stdin, e = os.Pipe()
	if e != nil {
		cleanup()
		return nil, opError("OpenControl", e)
	}
	c.stdout, outChild, e = os.Pipe()
	if e != nil {
		cleanup()
		return nil, opError("OpenControl", e)
	}
	c.stderr, errChild, e = os.Pipe()
	if e != nil {
		cleanup()
		return nil, opError("OpenControl", e)
	}
	c.cmd.Stdin = inChild
	c.cmd.Stdout = outChild
	c.cmd.Stderr = errChild
	c.diagnostics = process.NewBuffer(s.config.Limits.OutputBytes, func() { c.stop(ErrOutputLimit, false) })
	if e = c.cmd.Start(); e != nil {
		cleanup()
		return nil, opError("OpenControl", e)
	}
	_ = inChild.Close()
	_ = outChild.Close()
	_ = errChild.Close()
	c.work.Go(c.reader)
	c.work.Go(func() {
		_, e := io.Copy(c.diagnostics, c.stderr)
		if e != nil && !errors.Is(e, os.ErrClosed) && c.closedError() == nil {
			c.stop(e, false)
		}
	})
	c.work.Go(func() {
		e := c.cmd.Wait()
		if c.closedError() == nil {
			c.stop(errors.Join(ErrClosed, e), false)
		}
	})
	c.work.Go(func() { c.dispatch(nonce, session.h.guard()) })
	c.work.Go(func() {
		<-cctx.Done()
		if c.closedError() == nil {
			c.stop(context.Cause(cctx), false)
		}
	})
	go func() { c.work.Wait(); close(c.done) }()
	select {
	case e := <-c.ready:
		if e != nil {
			c.stop(e, false)
			closeErr := c.Close()
			return nil, opError("OpenControl", errors.Join(e, closeErr))
		}
		return c, nil
	case <-ctx.Done():
		c.stop(ctx.Err(), false)
		e = c.Close()
		return nil, opError("OpenControl", errors.Join(ctx.Err(), e))
	}
}
func (c *Connection) Server() *Server {
	if c == nil {
		return nil
	}
	return c.server
}

// AuxiliaryServer explicitly selects bounded subprocess execution but retains
// this connection's original daemon identity and lifetime. It is never used as
// an automatic retry/fallback and cannot start a replacement daemon.
func (c *Connection) AuxiliaryServer() *Server {
	if c == nil || c.original == nil {
		return nil
	}
	s := *c.original
	s.bound = &c.identity
	s.lifetime = c
	s.conn = nil
	return &s
}
func (c *Connection) closedError() error {
	if c == nil || c.ctx == nil {
		return ErrInvalidHandle
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.Join(ErrClosed, c.terminal)
	}
	return nil
}
func (c *Connection) stop(err error, normal bool) {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.normal = normal
		c.terminal = err
		streams := make([]*EventStream, 0, len(c.streams))
		for s := range c.streams {
			streams = append(streams, s)
		}
		c.mu.Unlock()
		c.cancel(errors.Join(ErrClosed, err))
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.stdout != nil {
			_ = c.stdout.Close()
		}
		if c.stderr != nil {
			_ = c.stderr.Close()
		}
		for _, s := range streams {
			s.finish(errors.Join(ErrClosed, err))
		}
	})
}

// Close is idempotent and concurrency-safe. It kills/reaps only the owned local
// control client. tmux's configured detach hooks and lifecycle policies remain
// the daemon's responsibility. Nil means all owned work has finished.
func (c *Connection) Close() error {
	if c == nil {
		return nil
	}
	if c.done == nil || c.cancel == nil {
		return ErrInvalidHandle
	}
	c.stop(nil, true)
	timer := time.NewTimer(process.ShutdownBudget)
	defer timer.Stop()
	select {
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.normal {
			return nil
		}
		return c.terminal
	case <-timer.C:
		return ErrShutdownIncomplete
	}
}
func (c *Connection) Wait(ctx context.Context) error {
	if c == nil || c.ctx == nil {
		return ErrInvalidHandle
	}
	if ctx == nil {
		return invalid("nil context")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.terminal
	}
}
func (c *Connection) Diagnostics() []byte {
	if c == nil || c.diagnostics == nil {
		return nil
	}
	return c.diagnostics.Bytes()
}
func (c *Connection) reader() {
	r := bufio.NewReaderSize(c.stdout, 32<<10)
	for {
		u, e := readControlUnit(r, c.opts.FrameBytes, c.publish)
		if e != nil {
			if c.closedError() == nil {
				c.stop(errors.Join(ErrProtocol, e), false)
			}
			return
		}
		if u.event != nil {
			c.publish(u.event)
			if u.event.RawName() == "exit" {
				c.stop(ErrClosed, false)
				return
			}
			continue
		}
		select {
		case c.frames <- *u.frame:
		case <-c.ctx.Done():
			return
		}
	}
}
func (c *Connection) collect(ctx context.Context, nonce string, outLimit, errLimit int64) (Result, error) {
	result := Result{Stdout: []byte{}, Stderr: []byte{}, ExitCode: -1}
	var failure error
	var drain <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	done := ctx.Done()
	appendData := func(dst *[]byte, data []byte, max int64) bool {
		left := max - int64(len(*dst))
		if int64(len(data)) > left {
			if left > 0 {
				*dst = append(*dst, data[:left]...)
			}
			return false
		}
		*dst = append(*dst, data...)
		return true
	}
	for {
		select {
		case <-c.ctx.Done():
			return result, errors.Join(failure, c.closedError())
		case <-done:
			failure = errors.Join(failure, ctx.Err())
			done = nil
			timer = time.NewTimer(process.ShutdownBudget)
			drain = timer.C
		case <-drain:
			c.stop(errors.Join(ErrProtocol, ErrShutdownIncomplete), false)
			return result, errors.Join(failure, ErrProtocol, ErrShutdownIncomplete)
		case f := <-c.frames:
			if bytes.Equal(f.data, []byte(nonce)) && !f.failed {
				return result, failure
			}
			if f.id.flags == 0 { // startup commands also carry flag zero; preserve their
				// guard acknowledgements, but never attribute unrelated hook diagnostics.
				if len(f.data) == 0 {
					continue
				}
				if !bytes.HasPrefix(f.data, []byte("TGO-GUARD-1:")) && !bytes.HasPrefix(f.data, []byte("TGO1:")) {
					if f.failed {
						failure = errors.Join(failure, ErrProtocol)
					}
					continue
				}
			}
			if f.failed {
				if !appendData(&result.Stderr, f.data, errLimit) {
					failure = errors.Join(failure, ErrOutputLimit)
				}
				failure = errors.Join(failure, errors.New("tmux control command failed"), classifyStderr(f.data))
			} else {
				if !appendData(&result.Stdout, f.data, outLimit) {
					failure = errors.Join(failure, ErrOutputLimit)
				}
			}
		}
	}
}
func (c *Connection) releaseRequest(r *controlRequest) { c.bytes.Release(r.bytes); c.count.Release(1) }
func (c *Connection) deliver(r *controlRequest, result Result, err error, effect Effect) {
	r.state.Store(2)
	if err != nil {
		err = &CommandError{Command: planName(r.plan), Result: cloneResult(result), Outcome: Outcome{Effect: effect}, Err: err}
	}
	r.result <- controlReply{result: result, err: err}
	c.releaseRequest(r)
}
func (c *Connection) dispatch(startNonce string, g *guard) {
	startup, cancel := context.WithTimeout(c.ctx, c.original.config.Limits.CommandTimeout)
	result, e := c.collect(startup, startNonce, c.original.config.Limits.OutputBytes, c.original.config.Limits.OutputBytes)
	cancel()
	_, e = g.unwrap(result, e)
	c.ready <- e
	if e != nil {
		c.stop(e, false)
	}
	defer func() {
		for {
			select {
			case r := <-c.requests:
				c.deliver(r, Result{ExitCode: -1}, c.closedError(), NotSent)
			default:
				return
			}
		}
	}()
	for {
		select {
		case <-c.ctx.Done():
			return
		case r := <-c.requests:
			if e := c.closedError(); e != nil {
				c.deliver(r, Result{ExitCode: -1}, e, NotSent)
				continue
			}
			if r.ctx.Err() != nil || !r.state.CompareAndSwap(0, 1) {
				c.deliver(r, Result{ExitCode: -1}, r.ctx.Err(), NotSent)
				continue
			}
			text, e := r.plan.text()
			if e != nil {
				c.deliver(r, Result{ExitCode: -1}, e, NotSent)
				continue
			}
			nonce, e := token("TGO-DONE:")
			if e != nil {
				c.deliver(r, Result{ExitCode: -1}, e, NotSent)
				continue
			}
			suffix, _ := emptyPlan(command("display-message", "-p", string(bytes.TrimSuffix([]byte(nonce), []byte{'\n'})))).text()
			if int64(len(text)+len(suffix)) > r.bytes {
				c.deliver(r, Result{ExitCode: -1}, ErrInputLimit, NotSent)
				continue
			}
			// The completion marker is a separate input line/group, so a nested command
			// error cannot remove it with tmux's remove-current-group behavior.
			wire := text + suffix
			deadline := time.Now().Add(process.ShutdownBudget)
			if d, ok := r.ctx.Deadline(); ok {
				deadline = d.Add(process.ShutdownBudget)
			}
			_ = c.stdin.SetWriteDeadline(deadline)
			_, e = io.WriteString(c.stdin, wire)
			if e != nil {
				c.stop(e, false)
				c.deliver(r, Result{ExitCode: -1}, e, Unknown)
				return
			}
			result, e := c.collect(r.ctx, nonce, r.stdout, r.stderr)
			effect := Unknown
			if e == nil {
				effect = Confirmed
			}
			c.deliver(r, result, e, effect)
		}
	}
}
func (c *Connection) run(op *operation, p plan, n int64) (Result, error) {
	failed := func(e error) (Result, error) {
		r := Result{ExitCode: -1}
		return r, &CommandError{Command: planName(p), Result: r, Outcome: Outcome{Effect: NotSent}, Err: e}
	}
	if p.mode == replyRaw {
		return failed(&UnsupportedError{Feature: "ambiguous raw/control output; explicitly use subprocess transport", Transport: Control, Err: ErrTransportUnsupported})
	}
	if n > c.opts.QueuedBytes {
		return failed(ErrResourceLimit)
	}
	ctx, cancel := context.WithCancel(op.ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	if e := c.count.Acquire(ctx, 1); e != nil {
		return failed(errors.Join(e, c.closedError()))
	}
	if e := c.bytes.Acquire(ctx, n); e != nil {
		c.count.Release(1)
		return failed(errors.Join(e, c.closedError()))
	}
	r := &controlRequest{ctx: op.ctx, plan: p, bytes: n, stdout: max(op.output, 0), stderr: max(op.stderr, 0), result: make(chan controlReply, 1)}
	c.mu.Lock()
	if c.closed {
		e := errors.Join(ErrClosed, c.terminal)
		c.mu.Unlock()
		c.releaseRequest(r)
		return failed(e)
	}
	c.requests <- r
	c.mu.Unlock()
	select {
	case v := <-r.result:
		return v.result, v.err
	case <-op.ctx.Done():
		effect := Unknown
		if r.state.CompareAndSwap(0, 3) {
			effect = NotSent
		}
		result := Result{ExitCode: -1}
		return result, &CommandError{Command: planName(p), Result: result, Outcome: Outcome{Effect: effect}, Timeout: contextSource(op.caller, op.ctx.Err()), Err: op.ctx.Err()}
	case <-c.ctx.Done():
		effect := Unknown
		if r.state.CompareAndSwap(0, 3) {
			effect = NotSent
		}
		result := Result{ExitCode: -1}
		return result, &CommandError{Command: planName(p), Result: result, Outcome: Outcome{Effect: effect}, Err: c.closedError()}
	}
}
