// Package exec owns bounded local tmux client execution. It never signals a
// daemon PID, changes the parent's environment, or uses a shell implicitly.
package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

const ShutdownBudget = 5 * time.Second

var ErrOutputLimit = errors.New("process output limit")
var ErrShutdownIncomplete = errors.New("process shutdown incomplete")

type Runner struct {
	Binary string
	Env    []string
	Dir    string
	slots  *semaphore.Weighted
}

func New(binary string, env []string, dir string, concurrent int) *Runner {
	return &Runner{Binary: binary, Env: append([]string{}, env...), Dir: dir, slots: semaphore.NewWeighted(int64(concurrent))}
}

type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
	Started        bool
	Err            error
}

// Buffer retains a bounded prefix and calls Overflow once. Write accepts and
// discards further bytes, so children can be reaped even after a limit failure.
type Buffer struct {
	mu         sync.Mutex
	b          bytes.Buffer
	Limit      int64
	Overflow   func()
	overflowed bool
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	n := len(p)
	left := b.Limit - int64(b.b.Len())
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
	f := b.Overflow
	b.mu.Unlock()
	if over && f != nil {
		f()
	}
	return n, nil
}
func (b *Buffer) Bytes() []byte    { b.mu.Lock(); defer b.mu.Unlock(); return bytes.Clone(b.b.Bytes()) }
func (b *Buffer) Overflowed() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.overflowed }

func (r *Runner) Run(ctx context.Context, args []string, input []byte, outMax, errMax int64) Result {
	result := Result{ExitCode: -1}
	if e := r.slots.Acquire(ctx, 1); e != nil {
		result.Err = e
		return result
	}
	defer r.slots.Release(1)
	if e := ctx.Err(); e != nil {
		result.Err = e
		return result
	}
	child, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	cmd := r.Command(child, args)
	stdout := &Buffer{Limit: outMax, Overflow: func() { cancel(ErrOutputLimit) }}
	stderr := &Buffer{Limit: errMax, Overflow: func() { cancel(ErrOutputLimit) }}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	e := cmd.Start()
	if e == nil {
		result.Started = true
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
	result.Err = e
	return result
}

// Command returns a configured, NOT STARTED process. Caller owns its lifecycle.
// It kills only its local client on cancellation. WaitDelay bounds pipe teardown.
func (r *Runner) Command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Env = append([]string{}, r.Env...)
	cmd.Dir = r.Dir
	cmd.WaitDelay = ShutdownBudget
	return cmd
}
func CopyBounded(dst *Buffer, src io.Reader) error { _, e := io.Copy(dst, src); return e }

func NewBuffer(limit int64, overflow func()) *Buffer {
	return &Buffer{Limit: limit, Overflow: overflow}
}
