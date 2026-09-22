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
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	defaultQueueDepth  = 64
	defaultQueuedBytes = 8 << 20
	defaultFrameBytes  = 4 << 20
	defaultEventBytes  = 32 << 20
	defaultMaxStreams  = 8
	envArgMultiplier   = 2
)

var (
	ErrControlCommandFailed = errors.New("tmux control command failed")
	connectionGeneration    atomic.Uint64
)

// ControlOptions configures limits and startup settings for an interactive control mode connection.
type ControlOptions struct {
	// PaneOutput enables terminal output events (%output). Defaults to false.
	PaneOutput bool

	// NoEcho requests -CC (control mode with echo disabled) instead of -C.
	// When enabled, terminal echo is disabled and framing differences are handled natively.
	NoEcho bool

	// Flags configures client startup flags (-f flag), such as [ClientFlagReadOnly]
	// or [ClientFlagWaitExit].
	Flags []ClientFlag

	// UTF8 specifies forced or disabled UTF-8 client mode (-u flag).
	UTF8 UTF8Mode

	// Colors256 enables 256-color support (-2 flag).
	Colors256 bool

	// TerminalFeatures sets terminal features overrides (-T flag).
	TerminalFeatures []string

	// QueueDepth is the maximum pending request queue depth. Defaults to 64.
	QueueDepth int

	// QueuedBytes bounds total pending request bytes. Defaults to 8 MiB.
	QueuedBytes int64

	// FrameBytes bounds the maximum individual control frame size. Defaults to 4 MiB.
	FrameBytes int64

	// EventBytes is the shared event capacity quota across all streams. Defaults to 32 MiB.
	EventBytes int64

	// MaxStreams is the maximum concurrent [EventStream] count. Defaults to 8.
	MaxStreams int
}

// ControlNewSessionOptions configures starting a new tmux session attached directly
// to a new control mode client connection.
type ControlNewSessionOptions struct {
	// Control configures the control connection limits, flags, and framing.
	Control ControlOptions

	// Name is the name for the new session (-s flag). If empty, tmux generates a name.
	Name string

	// Window is the name of the initial window (-n flag).
	Window string

	// Dir is the initial working directory for the session (-c flag).
	Dir string

	// Program is the command executed in the initial window.
	Program Program

	// TmuxEnv specifies native tmux environment variables for the new session (-e flag).
	TmuxEnv map[string]string

	// Size optionally sets initial session dimensions (-x, -y).
	Size Size

	// Group optionally adds the new session to an existing or new session group (-t flag).
	Group string

	// AttachIfExists causes new-session to attach to an existing session
	// of the same Name if one already exists (-A flag) instead of failing.
	AttachIfExists bool
}

// Connection manages an active, bidirectional tmux -C control mode client.
// Requests are dispatched sequentially over the wire. Handles created from this
// connection carry a unique [ServerIdentity.Generation] token to prevent reuse across restarts.
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
	diagnostics   *buffer
	stopOnce      sync.Once
	clientMu      sync.Mutex
	cachedClient  Client
	hasClient     bool
}

// OpenControl starts one owned control client attached to session. It does not
// create a scratch session, detach any other client, or reconnect. ctx owns the
// entire lifetime. The original Server continues to use subprocess execution.
func (s *Server) OpenControl(ctx context.Context, session Session, opts ControlOptions) (*Connection, error) {
	if err := s.validateOpenControl(ctx, session); err != nil {
		return nil, opError("OpenControl", err)
	}

	opts, err := normalizeControlOptions(opts)
	if err != nil {
		return nil, opError("OpenControl", err)
	}

	nonce, err := token("TGO-READY:")
	if err != nil {
		return nil, opError("OpenControl", err)
	}

	args, err := s.controlAttachArgs(session, opts, nonce)
	if err != nil {
		return nil, opError("OpenControl", err)
	}

	connCtx, cancel := context.WithCancelCause(ctx)
	c := newConnection(s, session, opts, cancel)

	if err = s.setupControlProcess(connCtx, c, args, cancel); err != nil {
		return nil, opError("OpenControl", err)
	}

	c.startWorkers(connCtx, nonce, session.h.guard())

	if err = c.awaitReady(ctx); err != nil {
		return nil, opError("OpenControl", err)
	}

	return c, nil
}

// OpenControlNewSession starts one owned control client by creating and attaching to a new session.
// It does not create a scratch session, and ctx owns the entire lifetime.
// The newly created [Session] is returned alongside the active [*Connection].
func validateControlNewSessionStrings(opts ControlNewSessionOptions) error {
	if opts.Name != "" && !wire.ValidString(opts.Name) {
		return invalid("session name")
	}

	if opts.Window != "" && !wire.ValidString(opts.Window) {
		return invalid("window name")
	}

	if opts.Dir != "" && !wire.ValidString(opts.Dir) {
		return invalid("dir")
	}

	if opts.Group != "" && !wire.ValidString(opts.Group) {
		return invalid("group")
	}

	return nil
}

func (s *Server) validateOpenControlNewSession(ctx context.Context, opts ControlNewSessionOptions) error {
	if s == nil || s.runner == nil {
		return ErrInvalidHandle
	}

	if s.conn != nil || s.bound != nil {
		return unsupportedControl("open control connection on control-bound server", ErrTransportUnsupported)
	}

	if ctx == nil || ctx.Err() != nil {
		return invalid("context")
	}

	if err := validateControlNewSessionStrings(opts); err != nil {
		return err
	}

	if !opts.Size.valid() {
		return invalid("size")
	}

	return nil
}

// OpenControlNewSession starts one owned control client by creating and attaching to a new session.
// It does not create a scratch session, and ctx owns the entire lifetime.
// The newly created [Session] is returned alongside the active [*Connection].
func (s *Server) OpenControlNewSession(ctx context.Context, opts ControlNewSessionOptions) (*Connection, Session, error) {
	if err := s.validateOpenControlNewSession(ctx, opts); err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	ctrlOpts, err := normalizeControlOptions(opts.Control)
	if err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	opts.Control = ctrlOpts

	nonce, err := token("TGO-READY:")
	if err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	args, err := s.controlNewSessionArgs(opts, nonce)
	if err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	info, err := s.Probe(ctx)
	if err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	connCtx, cancel := context.WithCancelCause(ctx)
	dummySession := Session{h: handle{server: nil, origin: info.Identity, id: "", kind: SessionKind, client: clientCheck{name: "", pid: 0, created: 0}}}

	c := newConnection(s, dummySession, opts.Control, cancel)
	if err = s.setupControlProcess(connCtx, c, args, cancel); err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	c.startWorkers(connCtx, nonce, nil)

	if err = c.awaitReady(ctx); err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	session, err := c.resolveCreatedSession(ctx)
	if err != nil {
		return nil, Session{}, opError("OpenControlNewSession", err)
	}

	return c, session, nil
}

// Client discovers and returns the exact [Client] handle for this control mode connection,
// matching the control process PID against the daemon's client list.
// Returns [ErrClosed] if terminating or [ErrNotFound] if detached.
func (c *Connection) Client(ctx context.Context) (Client, error) {
	if c == nil {
		return Client{}, opError("Connection.Client", ErrInvalidHandle)
	}

	if err := c.closedError(); err != nil {
		return Client{}, opError("Connection.Client", err)
	}

	if c.cmd == nil || c.cmd.Process == nil {
		return Client{}, opError("Connection.Client", ErrInvalidHandle)
	}

	c.clientMu.Lock()
	if c.hasClient {
		client := c.cachedClient
		c.clientMu.Unlock()

		return client, nil
	}
	c.clientMu.Unlock()

	pid := c.cmd.Process.Pid

	clients, err := c.Server().ClientsWith(ctx, QueryOptions{
		Filter:      Format(fmt.Sprintf("#{==:#{client_pid},%d}", pid)),
		ExtraFields: nil,
	})
	if err != nil {
		return Client{}, opError("Connection.Client", err)
	}

	if len(clients) == 0 {
		return Client{}, opError("Connection.Client", ErrNotFound)
	}

	client := clients[0].Handle()

	c.clientMu.Lock()
	c.cachedClient = client
	c.hasClient = true
	c.clientMu.Unlock()

	return client, nil
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

	timer := time.NewTimer(shutdownBudget)
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

func (c *Connection) resolveCreatedSession(ctx context.Context) (Session, error) {
	pid := c.cmd.Process.Pid

	clients, err := c.Server().ClientsWith(ctx, QueryOptions{
		Filter:      Format(fmt.Sprintf("#{==:#{client_pid},%d}", pid)),
		ExtraFields: nil,
	})
	if err != nil {
		_ = c.Close()
		return Session{}, err
	}

	if len(clients) == 0 {
		_ = c.Close()
		return Session{}, ErrNotFound
	}

	c.clientMu.Lock()
	c.cachedClient = clients[0].Handle()
	c.hasClient = true
	c.clientMu.Unlock()

	sid, ok := clients[0].SessionID.Get()
	if !ok {
		_ = c.Close()
		return Session{}, ErrNotFound
	}

	session, err := c.Server().Session(ctx, sid)
	if err != nil {
		_ = c.Close()
		return Session{}, err
	}

	return session, nil
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

		if c.stdout != nil && c.stdout != c.stdin {
			_ = c.stdout.Close()
		}

		if c.stderr != nil && c.stderr != c.stdout && c.stderr != c.stdin {
			_ = c.stderr.Close()
		}

		for _, s := range streams {
			s.finish(errors.Join(ErrClosed, err))
		}
	})
}

func validateControlLimits(opts ControlOptions) bool {
	if opts.QueueDepth < 0 || opts.QueuedBytes < 0 || opts.FrameBytes < 0 || opts.EventBytes < 0 || opts.MaxStreams < 0 {
		return false
	}

	if opts.QueueDepth > 1<<20 || opts.QueuedBytes > 1<<40 || opts.FrameBytes > 1<<40 || opts.EventBytes > 1<<40 || opts.MaxStreams > 1<<16 {
		return false
	}

	return true
}

func defaultControlOptions(opts ControlOptions) ControlOptions {
	if opts.QueueDepth == 0 {
		opts.QueueDepth = defaultQueueDepth
	}

	if opts.QueuedBytes == 0 {
		opts.QueuedBytes = defaultQueuedBytes
	}

	if opts.FrameBytes == 0 {
		opts.FrameBytes = defaultFrameBytes
	}

	if opts.EventBytes == 0 {
		opts.EventBytes = defaultEventBytes
	}

	if opts.MaxStreams == 0 {
		opts.MaxStreams = defaultMaxStreams
	}

	return opts
}

func normalizeControlOptions(opts ControlOptions) (ControlOptions, error) {
	if !validateControlLimits(opts) {
		return opts, invalid("control limits")
	}

	opts = defaultControlOptions(opts)

	if !validateControlLimits(opts) {
		return opts, invalid("control limits")
	}

	return opts, nil
}

func token(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	return prefix + hex.EncodeToString(b[:]) + "\n", nil
}

func newConnection(s *Server, session Session, opts ControlOptions, cancel context.CancelCauseFunc) *Connection {
	gen := connectionGeneration.Add(1)
	id := session.h.origin
	id.Generation = gen

	c := &Connection{
		server:        nil,
		original:      s,
		identity:      id,
		generation:    gen,
		opts:          opts,
		cancel:        cancel,
		stopCh:        make(chan struct{}),
		mu:            sync.Mutex{},
		closed:        false,
		terminal:      nil,
		normal:        false,
		streams:       map[*EventStream]struct{}{},
		eventReserved: 0,
		count:         semaphore.NewWeighted(int64(opts.QueueDepth)),
		bytes:         semaphore.NewWeighted(opts.QueuedBytes),
		requests:      make(chan *controlRequest, opts.QueueDepth),
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
		clientMu:      sync.Mutex{},
		cachedClient:  Client{}, //nolint:exhaustruct_v5 // initial connection state sets uninitialized client
		hasClient:     false,
	}
	server := *s
	server.conn = c
	server.bound = &c.identity
	server.lifetime = c
	c.server = &server

	return c
}

func (s *Server) controlRootArgs(opts ControlOptions) []string {
	cfg := s.config
	if opts.UTF8 != UTF8Default {
		cfg.UTF8 = opts.UTF8
	}

	if opts.Colors256 {
		cfg.Colors256 = true
	}

	if len(opts.TerminalFeatures) > 0 {
		cfg.TerminalFeatures = opts.TerminalFeatures
	}

	sOverride := *s
	sOverride.config = cfg
	rootArgs := sOverride.baseArgs(false)

	controlFlag := "-C"
	if opts.NoEcho {
		controlFlag = "-CC"
	}

	return append(rootArgs, controlFlag)
}

func (s *Server) controlFlags(opts ControlOptions) (string, error) {
	flags := []ClientFlag{ClientFlagIgnoreSize}
	if !opts.PaneOutput {
		flags = append(flags, ClientFlagNoOutput)
	}

	for _, f := range opts.Flags {
		if !f.Valid() {
			return "", invalid("client flag")
		}

		flags = append(flags, f)
	}

	return formatClientFlags(flags), nil
}

func (s *Server) controlAttachArgs(session Session, opts ControlOptions, nonce string) ([]string, error) {
	flagsStr, err := s.controlFlags(opts)
	if err != nil {
		return nil, err
	}

	p := session.h.guard().wrap(emptyPlan(command("attach-session", "-t", session.h.id, "-f", flagsStr)))
	p.nodes = append(p.nodes, markerNode(nonce))

	args, err := p.argv()
	if err != nil {
		return nil, err
	}

	return append(s.controlRootArgs(opts), args...), nil
}

func newSessionFlags(opts ControlNewSessionOptions) []string {
	var args []string
	if opts.AttachIfExists {
		args = append(args, "-A")
	}

	if opts.Name != "" {
		args = append(args, "-s", opts.Name)
	}

	if opts.Window != "" {
		args = append(args, "-n", opts.Window)
	}

	if opts.Dir != "" {
		args = append(args, "-c", opts.Dir)
	}

	if opts.Group != "" {
		args = append(args, "-t", opts.Group)
	}

	if opts.Size.Width > 0 {
		args = append(args, "-x", strconv.Itoa(opts.Size.Width))
	}

	if opts.Size.Height > 0 {
		args = append(args, "-y", strconv.Itoa(opts.Size.Height))
	}

	return args
}

func newSessionEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	args := make([]string, 0, len(keys)*envArgMultiplier)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}

	return args
}

func (s *Server) controlNewSessionArgs(opts ControlNewSessionOptions, nonce string) ([]string, error) {
	flagsStr, err := s.controlFlags(opts.Control)
	if err != nil {
		return nil, err
	}

	cmdArgs := append([]string{"new-session", "-f", flagsStr}, newSessionFlags(opts)...)

	cmdArgs = append(cmdArgs, newSessionEnv(opts.TmuxEnv)...)

	progArgs, err := opts.Program.argv()
	if err != nil {
		return nil, err
	}

	cmdArgs = append(cmdArgs, progArgs...)

	p := emptyPlan(command(cmdArgs[0], cmdArgs[1:]...))
	p.nodes = append(p.nodes, markerNode(nonce))

	args, err := p.argv()
	if err != nil {
		return nil, err
	}

	return append(s.controlRootArgs(opts.Control), args...), nil
}

func (s *Server) setupControlProcess(connCtx context.Context, c *Connection, args []string, cancel context.CancelCauseFunc) error {
	c.cmd = s.runner.command(connCtx, args)

	if c.opts.NoEcho {
		master, slave, err := openPTY()
		if err != nil {
			cancel(ErrClosed)
			return err
		}

		errRead, errChild, err := os.Pipe()
		if err != nil {
			_ = master.Close()
			_ = slave.Close()

			cancel(ErrClosed)

			return err //nolint:wrapcheck // OS pipe error is propagated directly to caller
		}

		c.stdin = master
		c.stdout = master
		c.stderr = errRead

		c.cmd.Stdin = slave
		c.cmd.Stdout = slave
		c.cmd.Stderr = errChild
		c.diagnostics = newBuffer(s.config.Limits.OutputBytes, func() { c.stop(ErrOutputLimit, false) })

		if err = c.cmd.Start(); err != nil {
			_ = master.Close()
			_ = slave.Close()
			_ = errRead.Close()
			_ = errChild.Close()

			cancel(ErrClosed)

			return err //nolint:wrapcheck // command start error is propagated directly to caller
		}

		_ = slave.Close()
		_ = errChild.Close()

		return nil
	}

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
	c.diagnostics = newBuffer(s.config.Limits.OutputBytes, func() { c.stop(ErrOutputLimit, false) })

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

func (c *Connection) startWorkers(connCtx context.Context, nonce string, guard *guard) {
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
	c.work.Go(func() { c.dispatch(connCtx, nonce, guard) })

	c.work.Go(func() {
		<-connCtx.Done()

		if c.closedError() == nil {
			c.stop(context.Cause(connCtx), false)
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
