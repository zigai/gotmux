package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	process "github.com/zigai/gotmux/internal/exec"
)

const (
	defaultQueueDepth  = 64
	defaultQueuedBytes = 8 << 20
	defaultFrameBytes  = 4 << 20
	defaultEventBytes  = 32 << 20
	defaultMaxStreams  = 8
)

var (
	ErrControlCommandFailed = errors.New("tmux control command failed")
	connectionGeneration    atomic.Uint64
)

// ControlOptions configures resource limits and behaviors for an interactive control mode connection.
type ControlOptions struct {
	// PaneOutput enables asynchronous terminal output events (%output and %extended-output)
	// from panes. Disabled by default to prevent saturating the control wire with terminal stream noise.
	PaneOutput bool

	// QueueDepth is the maximum number of requests that may be queued waiting for dispatch
	// before admission fails with [ErrResourceLimit]. Defaults to 64.
	QueueDepth int

	// QueuedBytes bounds the total memory held by pending request payloads in the queue.
	// Defaults to 8 MiB.
	QueuedBytes int64

	// FrameBytes bounds the maximum size of a single control notification or response frame
	// read from tmux before failing the connection with [ErrProtocol]. Defaults to 4 MiB.
	FrameBytes int64

	// EventBytes is the total byte budget shared across all active [EventStream] reservations.
	// Defaults to 32 MiB.
	EventBytes int64

	// MaxStreams is the maximum number of concurrent [EventStream] instances permitted.
	// Defaults to 8.
	MaxStreams int
}

// Connection manages an active, bidirectional tmux -C control mode client.
//
// Concurrency model:
// A Connection owns exactly one reader goroutine, one writer/dispatcher goroutine, and
// an event distribution hub. Only one request is dispatched to tmux at a time; subsequent
// requests wait in a bounded admission queue.
//
// Generation safety:
// Handles created from this connection carry a unique, process-local [ServerIdentity.Generation]
// token so they cannot be accidentally reused if the connection terminates and restarts.
type Connection struct {
	server        *Server
	original      *Server
	identity      ServerIdentity
	generation    uint64
	opts          ControlOptions
	cancel        context.CancelCauseFunc
	stopCh        chan struct{}
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
	work          sync.WaitGroup
	cmd           *exec.Cmd
	stdin         *os.File
	stdout        *os.File
	stderr        *os.File
	diagnostics   *process.Buffer
	stopOnce      sync.Once
}

// OpenControl starts one owned control client attached to session. It does not
// create a scratch session, detach any other client, or reconnect. ctx owns the
// entire lifetime. The original Server continues to use subprocess execution.
func (s *Server) OpenControl(ctx context.Context, session Session, o ControlOptions) (*Connection, error) {
	if err := s.validateOpenControl(ctx, session); err != nil {
		return nil, opError("OpenControl", err)
	}

	o, err := normalizeControlOptions(o)
	if err != nil {
		return nil, opError("OpenControl", err)
	}

	nonce, err := token("TGO-READY:")
	if err != nil {
		return nil, opError("OpenControl", err)
	}

	args, err := s.controlAttachArgs(session, o, nonce)
	if err != nil {
		return nil, opError("OpenControl", err)
	}

	cctx, cancel := context.WithCancelCause(ctx)
	c := newConnection(s, session, o, cancel)

	if err = s.setupControlProcess(c, cctx, args, cancel); err != nil {
		return nil, opError("OpenControl", err)
	}

	c.startWorkers(cctx, nonce, session.h.guard())

	if err = c.awaitReady(ctx); err != nil {
		return nil, opError("OpenControl", err)
	}

	return c, nil
}

// Server returns a [*Server] whose operations are dispatched through this control connection wire.
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

// Wait blocks until the control connection terminates, either normally via [Connection.Close]
// or unexpectedly due to daemon termination, network/pipe errors, or protocol violations.
// Returns nil on clean normal closure, or the terminal error that caused the connection to fail.
func (c *Connection) Wait(ctx context.Context) error {
	if c == nil || c.done == nil {
		return ErrInvalidHandle
	}

	if ctx == nil {
		return invalid("nil context")
	}

	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // context cancellation is intentionally returned unwrapped
	case <-c.done:
		return c.terminal
	}
}

// Diagnostics returns a copy of captured stderr bytes emitted by the tmux control client subprocess.
func (c *Connection) Diagnostics() []byte {
	if c == nil || c.diagnostics == nil {
		return nil
	}

	return c.diagnostics.Bytes()
}

func (c *Connection) closedError() error {
	if c == nil || c.done == nil {
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
		close(c.stopCh)
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

func validateControlLimits(o ControlOptions) bool {
	if o.QueueDepth < 0 || o.QueuedBytes < 0 || o.FrameBytes < 0 || o.EventBytes < 0 || o.MaxStreams < 0 {
		return false
	}

	if o.QueueDepth > 1<<20 || o.QueuedBytes > 1<<40 || o.FrameBytes > 1<<40 || o.EventBytes > 1<<40 || o.MaxStreams > 1<<16 {
		return false
	}

	return true
}

func defaultControlOptions(o ControlOptions) ControlOptions {
	if o.QueueDepth == 0 {
		o.QueueDepth = defaultQueueDepth
	}

	if o.QueuedBytes == 0 {
		o.QueuedBytes = defaultQueuedBytes
	}

	if o.FrameBytes == 0 {
		o.FrameBytes = defaultFrameBytes
	}

	if o.EventBytes == 0 {
		o.EventBytes = defaultEventBytes
	}

	if o.MaxStreams == 0 {
		o.MaxStreams = defaultMaxStreams
	}

	return o
}

func normalizeControlOptions(o ControlOptions) (ControlOptions, error) {
	if !validateControlLimits(o) {
		return o, invalid("control limits")
	}

	o = defaultControlOptions(o)

	if !validateControlLimits(o) {
		return o, invalid("control limits")
	}

	return o, nil
}

func token(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	return prefix + hex.EncodeToString(b[:]) + "\n", nil
}

func newConnection(s *Server, session Session, o ControlOptions, cancel context.CancelCauseFunc) *Connection {
	gen := connectionGeneration.Add(1)
	id := session.h.origin
	id.Generation = gen

	c := &Connection{
		server:        nil,
		original:      s,
		identity:      id,
		generation:    gen,
		opts:          o,
		cancel:        cancel,
		stopCh:        make(chan struct{}),
		mu:            sync.Mutex{},
		closed:        false,
		terminal:      nil,
		normal:        false,
		streams:       map[*EventStream]struct{}{},
		eventReserved: 0,
		count:         semaphore.NewWeighted(int64(o.QueueDepth)),
		bytes:         semaphore.NewWeighted(o.QueuedBytes),
		requests:      make(chan *controlRequest, o.QueueDepth),
		frames:        make(chan controlFrame, 1),
		ready:         make(chan error, 1),
		done:          make(chan struct{}),
		work:          sync.WaitGroup{},
		cmd:           nil,
		stdin:         nil,
		stdout:        nil,
		stderr:        nil,
		diagnostics:   nil,
		stopOnce:      sync.Once{},
	}

	server := *s
	server.conn = c
	server.bound = &c.identity
	server.lifetime = c
	c.server = &server

	return c
}

func (s *Server) controlAttachArgs(session Session, o ControlOptions, nonce string) ([]string, error) {
	flags := "ignore-size"
	if !o.PaneOutput {
		flags += ",no-output"
	}

	p := session.h.guard().wrap(emptyPlan(command("attach-session", "-t", session.h.id, "-f", flags)))
	p.nodes = append(p.nodes, markerNode(nonce))

	args, err := p.argv()
	if err != nil {
		return nil, err
	}

	return append(append(s.baseArgs(false), "-C"), args...), nil
}

func (s *Server) setupControlProcess(c *Connection, cctx context.Context, args []string, cancel context.CancelCauseFunc) error {
	c.cmd = s.runner.Command(cctx, args)

	inChild, inWrite, err := os.Pipe()
	if err != nil {
		cancel(ErrClosed)
		return err //nolint:wrapcheck // OS pipe error is propagated directly to caller
	}

	outRead, outChild, err := os.Pipe()
	if err != nil {
		_ = inChild.Close()
		_ = inWrite.Close()

		cancel(ErrClosed)

		return err //nolint:wrapcheck // OS pipe error is propagated directly to caller
	}

	errRead, errChild, err := os.Pipe()
	if err != nil {
		_ = inChild.Close()
		_ = inWrite.Close()
		_ = outRead.Close()
		_ = outChild.Close()

		cancel(ErrClosed)

		return err //nolint:wrapcheck // OS pipe error is propagated directly to caller
	}

	c.stdin = inWrite
	c.stdout = outRead
	c.stderr = errRead

	c.cmd.Stdin = inChild
	c.cmd.Stdout = outChild
	c.cmd.Stderr = errChild

	c.diagnostics = process.NewBuffer(s.config.Limits.OutputBytes, func() { c.stop(ErrOutputLimit, false) })
	if err = c.cmd.Start(); err != nil {
		_ = inChild.Close()
		_ = inWrite.Close()
		_ = outRead.Close()
		_ = outChild.Close()
		_ = errRead.Close()
		_ = errChild.Close()

		cancel(ErrClosed)

		return err //nolint:wrapcheck // command start error is propagated directly to caller
	}

	_ = inChild.Close()
	_ = outChild.Close()
	_ = errChild.Close()

	return nil
}

func (s *Server) validateOpenControl(ctx context.Context, session Session) error {
	if s == nil || s.runner == nil {
		return ErrInvalidHandle
	}

	if ctx == nil {
		return invalid("nil context")
	}

	if err := ctx.Err(); err != nil {
		return err //nolint:wrapcheck // context cancellation is intentionally returned unwrapped
	}

	if err := session.h.check(); err != nil {
		return err
	}

	if s.conn != nil || session.h.origin.Generation != 0 || session.h.origin.Endpoint != s.endpoint {
		return ErrInvalidHandle
	}

	return nil
}

func (c *Connection) startWorkers(cctx context.Context, nonce string, guard *guard) {
	c.work.Go(c.reader)
	c.work.Go(func() {
		_, copyErr := io.Copy(c.diagnostics, c.stderr)
		if copyErr != nil && !errors.Is(copyErr, os.ErrClosed) && c.closedError() == nil {
			c.stop(copyErr, false)
		}
	})
	c.work.Go(func() {
		waitErr := c.cmd.Wait()
		if c.closedError() == nil {
			c.stop(errors.Join(ErrClosed, waitErr), false)
		}
	})
	c.work.Go(func() { c.dispatch(cctx, nonce, guard) })

	c.work.Go(func() {
		<-cctx.Done()

		if c.closedError() == nil {
			c.stop(context.Cause(cctx), false)
		}
	})
	go func() { c.work.Wait(); close(c.done) }()
}

func (c *Connection) awaitReady(ctx context.Context) error {
	select {
	case err := <-c.ready:
		if err != nil {
			c.stop(err, false)
			closeErr := c.Close()

			return errors.Join(err, closeErr)
		}

		return nil
	case <-ctx.Done():
		c.stop(ctx.Err(), false)
		closeErr := c.Close()

		return errors.Join(ctx.Err(), closeErr)
	}
}
