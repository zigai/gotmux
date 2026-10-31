package tmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
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
	//exhaustruct:optional
	mu sync.Mutex
	//exhaustruct:optional
	b        bytes.Buffer
	limit    int64
	overflow func()
	//exhaustruct:optional
	overflowed bool
}

type commandOutput struct {
	//exhaustruct:optional
	readers []*os.File
	//exhaustruct:optional
	writers []*os.File
	//exhaustruct:optional
	copying sync.WaitGroup
}

func newBuffer(limit int64, overflow func()) *buffer {
	return &buffer{
		limit:    limit,
		overflow: overflow,
	}
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
	return &runner{
		binary: binary,
		env:    append([]string{}, env...),
		dir:    dir,
		slots:  semaphore.NewWeighted(int64(concurrent)),
	}
}

func (r *runner) run(
	ctx context.Context,
	args []string,
	input []byte,
	outMax int64,
	errMax int64,
) (Result, bool, error) {
	result := Result{Stdout: nil, Stderr: nil, ExitCode: -1}
	if e := r.slots.Acquire(ctx, 1); e != nil {
		return result, false, e //nolint:wrapcheck // semaphore error is propagated directly
	}
	defer r.slots.Release(1)

	if e := ctx.Err(); e != nil {
		return result, false, e //nolint:wrapcheck // context error is propagated directly
	}

	cmdCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	cmd := r.command(cmdCtx, args)
	stdout := newBuffer(outMax, func() { cancel(ErrOutputLimit) })
	stderr := newBuffer(errMax, func() { cancel(ErrOutputLimit) })

	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	started := false

	output, e := openOutput(cmd, stdout, stderr)
	if e == nil {
		e = cmd.Start()

		output.closeWriters()

		started = e == nil
		if started {
			e = cmd.Wait()
		}

		e = errors.Join(e, output.drain(cmdCtx))
	}

	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if cause := context.Cause(cmdCtx); cause != nil {
		e = errors.Join(e, cause)
	}

	if errors.Is(e, exec.ErrWaitDelay) {
		e = errors.Join(e, ErrShutdownIncomplete)
	}

	return result, started, e
}

func openOutput(cmd *exec.Cmd, stdout io.Writer, stderr io.Writer) (*commandOutput, error) {
	output := &commandOutput{}

	for _, sink := range []io.Writer{stdout, stderr} {
		reader, writer, e := os.Pipe()
		if e != nil {
			output.closeWriters()
			output.closeReaders()

			return nil, e //nolint:wrapcheck // pipe creation error is propagated directly
		}

		output.readers = append(output.readers, reader)
		output.writers = append(output.writers, writer)
		output.copying.Go(func() { _, _ = io.Copy(sink, reader) })
	}

	cmd.Stdout, cmd.Stderr = output.writers[0], output.writers[1]

	return output, nil
}

func (o *commandOutput) drain(ctx context.Context) error {
	finished := make(chan struct{})

	go func() {
		o.copying.Wait()
		close(finished)
	}()

	var e error

	if ctx.Err() == nil {
		timer := time.NewTimer(shutdownBudget)
		defer timer.Stop()

		select {
		case <-finished:
		case <-timer.C:
			e = ErrShutdownIncomplete
		}
	}

	o.closeReaders()
	<-finished

	return e
}

func (o *commandOutput) closeWriters() {
	for _, writer := range o.writers {
		_ = writer.Close()
	}
}

func (o *commandOutput) closeReaders() {
	for _, reader := range o.readers {
		_ = reader.Close()
	}
}

func (r *runner) command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Env = append([]string{}, r.env...)
	cmd.Dir = r.dir
	cmd.WaitDelay = shutdownBudget

	return cmd
}
