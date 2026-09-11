package tmux

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

const shutdownBudget = 5 * time.Second

type runner struct {
	binary string
	env    []string
	dir    string
	slots  *semaphore.Weighted
}

type buffer struct {
	mu         sync.Mutex
	b          bytes.Buffer
	limit      int64
	overflow   func()
	overflowed bool
}

func newBuffer(limit int64, overflow func()) *buffer {
	return &buffer{limit: limit, overflow: overflow, mu: sync.Mutex{}, b: bytes.Buffer{}, overflowed: false}
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	n := len(p)
	left := b.limit - int64(b.b.Len())

	take := int64(n)
	if take > left {
		take = max(left, 0)
	}

	if take > 0 {
		b.b.Write(p[:int(take)])
	}

	over := int64(n) > left && !b.overflowed
	if over {
		b.overflowed = true
	}

	f := b.overflow
	b.mu.Unlock()

	if over && f != nil {
		f()
	}

	return n, nil
}

func (b *buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	return bytes.Clone(b.b.Bytes())
}

func (b *buffer) Overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.overflowed
}

func newRunner(binary string, env []string, dir string, concurrent int) *runner {
	return &runner{binary: binary, env: append([]string{}, env...), dir: dir, slots: semaphore.NewWeighted(int64(concurrent))}
}

func (r *runner) run(ctx context.Context, args []string, input []byte, outMax, errMax int64) (Result, bool, error) {
	result := Result{Stdout: nil, Stderr: nil, ExitCode: -1}
	if e := r.slots.Acquire(ctx, 1); e != nil {
		return result, false, e //nolint:wrapcheck // semaphore error is propagated directly
	}
	defer r.slots.Release(1)

	if e := ctx.Err(); e != nil {
		return result, false, e //nolint:wrapcheck // context error is propagated directly
	}

	child, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	cmd := r.command(child, args)
	stdout := newBuffer(outMax, func() { cancel(ErrOutputLimit) })
	stderr := newBuffer(errMax, func() { cancel(ErrOutputLimit) })
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	started := false

	e := cmd.Start()
	if e == nil {
		started = true
		e = cmd.Wait()
	}

	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if cause := context.Cause(child); cause != nil {
		e = errors.Join(e, cause)
	}

	if errors.Is(e, exec.ErrWaitDelay) {
		e = errors.Join(e, ErrShutdownIncomplete)
	}

	return result, started, e
}

func (r *runner) command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Env = append([]string{}, r.env...)
	cmd.Dir = r.dir
	cmd.WaitDelay = shutdownBudget

	return cmd
}
