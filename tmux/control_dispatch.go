package tmux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	process "github.com/zigai/gotmux/internal/exec"
)

const readerBufferSize = 32 << 10

const (
	requestQueued uint32 = iota
	requestInFlight
	requestDelivered
	requestCanceled
)

const controlWireOverhead int64 = 128

type controlRequest struct {
	done     <-chan struct{}
	deadline time.Time
	plan     plan
	bytes    int64
	stdout   int64
	stderr   int64
	result   chan controlReply
	state    atomic.Uint32
}

type controlReply struct {
	result Result
	err    error
}

func (c *Connection) reader() {
	r := bufio.NewReaderSize(c.stdout, readerBufferSize)
	for {
		unit, err := readControlUnit(r, c.opts.FrameBytes, c.publish)
		if err != nil {
			if c.closedError() == nil {
				c.stop(errors.Join(ErrProtocol, err), false)
			}

			return
		}

		if unit.event != nil {
			c.publish(unit.event)

			if unit.event.RawName() == "exit" {
				c.stop(ErrClosed, false)
				return
			}

			continue
		}

		select {
		case c.frames <- *unit.frame:
		case <-c.stopCh:
			return
		}
	}
}

func (c *Connection) collect(ctx context.Context, nonce string, outLimit, errLimit int64) (Result, error) {
	return c.collectRequest(ctx.Done(), nonce, outLimit, errLimit)
}

func (c *Connection) collectRequest(done <-chan struct{}, nonce string, outLimit, errLimit int64) (Result, error) {
	result := Result{Stdout: []byte{}, Stderr: []byte{}, ExitCode: -1}

	var (
		failure error
		drain   <-chan time.Time
		timer   *time.Timer
	)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-c.stopCh:
			return result, errors.Join(failure, c.closedError())
		case <-done:
			failure = errors.Join(failure, context.Canceled)
			done = nil
			timer = time.NewTimer(process.ShutdownBudget)
			drain = timer.C
		case <-drain:
			c.stop(errors.Join(ErrProtocol, ErrShutdownIncomplete), false)
			return result, errors.Join(failure, ErrProtocol, ErrShutdownIncomplete)
		case f := <-c.frames:
			if c.handleCollectFrame(f, nonce, &result, outLimit, errLimit, &failure) {
				return result, failure
			}
		}
	}
}

func appendFrameData(dst *[]byte, data []byte, limit int64) bool {
	left := limit - int64(len(*dst))
	if int64(len(data)) > left {
		if left > 0 {
			*dst = append(*dst, data[:left]...)
		}

		return false
	}

	*dst = append(*dst, data...)

	return true
}

func (c *Connection) handleCollectFrame(f controlFrame, nonce string, result *Result, outLimit, errLimit int64, failure *error) bool {
	if bytes.Equal(f.data, []byte(nonce)) && !f.failed {
		return true
	}

	if f.id.flags == 0 {
		if len(f.data) == 0 {
			return false
		}

		if !bytes.HasPrefix(f.data, []byte("TGO-GUARD-1:")) && !bytes.HasPrefix(f.data, []byte("TGO1:")) {
			if f.failed {
				*failure = errors.Join(*failure, ErrProtocol)
			}

			return false
		}
	}

	if f.failed {
		if !appendFrameData(&result.Stderr, f.data, errLimit) {
			*failure = errors.Join(*failure, ErrOutputLimit)
		}

		*failure = errors.Join(*failure, ErrControlCommandFailed, classifyStderr(f.data))
	} else if !appendFrameData(&result.Stdout, f.data, outLimit) {
		*failure = errors.Join(*failure, ErrOutputLimit)
	}

	return false
}

func (c *Connection) releaseRequest(r *controlRequest) { c.bytes.Release(r.bytes); c.count.Release(1) }

func (c *Connection) deliver(r *controlRequest, result Result, err error, effect Effect) {
	r.state.Store(requestDelivered)

	if err != nil {
		err = &CommandError{Command: planName(r.plan), Result: cloneResult(result), Outcome: Outcome{Effect: effect, Steps: nil, Created: nil}, Timeout: NoTimeout, Err: err}
	}

	r.result <- controlReply{result: result, err: err}

	c.releaseRequest(r)
}

func (c *Connection) performStartupHandshake(ctx context.Context, nonce string, g *guard) error {
	startup, cancel := context.WithTimeout(ctx, c.original.config.Limits.CommandTimeout)
	defer cancel()

	result, err := c.collect(startup, nonce, c.original.config.Limits.OutputBytes, c.original.config.Limits.OutputBytes)

	_, err = g.unwrap(result, err)
	c.ready <- err

	if err != nil {
		c.stop(err, false)
	}

	return err
}

func (c *Connection) drainRequests() {
	for {
		select {
		case r := <-c.requests:
			c.deliver(r, failedResult(), c.closedError(), NotSent)
		default:
			return
		}
	}
}

func buildRequestWire(r *controlRequest) (string, string, error) {
	text, err := r.plan.text()
	if err != nil {
		return "", "", err
	}

	nonce, err := token("TGO-DONE:")
	if err != nil {
		return "", "", err
	}

	suffix, _ := emptyPlan(command("display-message", "-p", string(bytes.TrimSuffix([]byte(nonce), []byte{'\n'})))).text()
	if int64(len(text)+len(suffix)) > r.bytes {
		return "", "", ErrInputLimit
	}

	return text + suffix, nonce, nil
}

func (c *Connection) dispatchRequest(r *controlRequest) bool {
	if err := c.closedError(); err != nil {
		c.deliver(r, failedResult(), err, NotSent)
		return true
	}

	select {
	case <-r.done:
		c.deliver(r, failedResult(), context.Canceled, NotSent)
		return true
	default:
	}

	if !r.state.CompareAndSwap(requestQueued, requestInFlight) {
		c.deliver(r, failedResult(), context.Canceled, NotSent)
		return true
	}

	wire, nonce, err := buildRequestWire(r)
	if err != nil {
		c.deliver(r, failedResult(), err, NotSent)
		return true
	}

	deadline := time.Now().Add(process.ShutdownBudget)
	if !r.deadline.IsZero() {
		deadline = r.deadline.Add(process.ShutdownBudget)
	}

	_ = c.stdin.SetWriteDeadline(deadline)

	if _, err := io.WriteString(c.stdin, wire); err != nil {
		c.stop(err, false)
		c.deliver(r, failedResult(), err, Unknown)

		return false
	}

	result, err := c.collectRequest(r.done, nonce, r.stdout, r.stderr)

	effect := Unknown
	if err == nil {
		effect = Confirmed
	}

	c.deliver(r, result, err, effect)

	return true
}

func (c *Connection) dispatch(ctx context.Context, nonce string, g *guard) {
	if err := c.performStartupHandshake(ctx, nonce, g); err != nil {
		return
	}

	defer c.drainRequests()

	for {
		select {
		case <-ctx.Done():
			return
		case r := <-c.requests:
			if !c.dispatchRequest(r) {
				return
			}
		}
	}
}

func (c *Connection) run(ctx context.Context, op *operation, p plan, n int64) (Result, error) {
	failed := func(err error) (Result, error) {
		r := failedResult()
		return r, &CommandError{Command: planName(p), Result: r, Outcome: Outcome{Effect: NotSent, Steps: nil, Created: nil}, Timeout: NoTimeout, Err: err}
	}
	if p.mode == replyRaw {
		return failed(unsupportedTransport("ambiguous raw/control output; explicitly use subprocess transport", Control, ErrTransportUnsupported))
	}

	if n > c.opts.QueuedBytes {
		return failed(ErrResourceLimit)
	}

	runCtx, cancel := context.WithCancel(ctx)

	go func() {
		select {
		case <-c.stopCh:
			cancel()
		case <-runCtx.Done():
		}
	}()

	defer cancel()

	if err := c.acquireCapacity(runCtx, n); err != nil {
		return failed(err)
	}

	r := &controlRequest{
		done:     runCtx.Done(),
		deadline: requestDeadline(runCtx),
		plan:     p,
		bytes:    n,
		stdout:   max(op.output, 0),
		stderr:   max(op.stderr, 0),
		result:   make(chan controlReply, 1),
		state:    atomic.Uint32{},
	}

	if err := c.enqueueRequest(r); err != nil {
		return failed(err)
	}

	select {
	case v := <-r.result:
		return v.result, v.err
	case <-ctx.Done():
		effect := Unknown
		if r.state.CompareAndSwap(requestQueued, requestCanceled) {
			effect = NotSent
		}

		result := failedResult()

		return result, &CommandError{Command: planName(p), Result: result, Outcome: Outcome{Effect: effect, Steps: nil, Created: nil}, Timeout: contextSource(op.callerDone, ctx.Err()), Err: ctx.Err()}
	case <-c.stopCh:
		effect := Unknown
		if r.state.CompareAndSwap(requestQueued, requestCanceled) {
			effect = NotSent
		}

		result := failedResult()

		return result, &CommandError{Command: planName(p), Result: result, Outcome: Outcome{Effect: effect, Steps: nil, Created: nil}, Timeout: NoTimeout, Err: c.closedError()}
	}
}

func (c *Connection) acquireCapacity(ctx context.Context, n int64) error {
	if err := c.count.Acquire(ctx, 1); err != nil {
		return errors.Join(err, c.closedError())
	}

	if err := c.bytes.Acquire(ctx, n); err != nil {
		c.count.Release(1)
		return errors.Join(err, c.closedError())
	}

	return nil
}

func requestDeadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}

	return time.Time{}
}

func (c *Connection) enqueueRequest(r *controlRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		c.releaseRequest(r)
		return errors.Join(ErrClosed, c.terminal)
	}

	c.requests <- r

	return nil
}
