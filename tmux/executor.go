package tmux

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
)

type operation struct {
	callerDone <-chan struct{}
	close      context.CancelFunc
	output     int64
	stderr     int64
	input      int64
}

func (s *Server) begin(ctx context.Context) (context.Context, *operation, error) {
	if s == nil || s.runner == nil {
		return nil, nil, ErrInvalidHandle
	}

	if ctx == nil {
		return nil, nil, invalid("nil context")
	}

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, nil, unsupported("platform " + runtime.GOOS)
	}

	if s.lifetime != nil {
		if err := s.lifetime.closedError(); err != nil {
			return nil, nil, err
		}
	}

	child, cancel := context.WithTimeout(ctx, s.config.Limits.CommandTimeout)

	return child, &operation{
		callerDone: ctx.Done(),
		close:      cancel,
		output:     s.config.Limits.OutputBytes,
		stderr:     s.config.Limits.OutputBytes,
		input:      s.config.Limits.InputBytes,
	}, nil
}

func (s *Server) executeProcess(ctx context.Context, op *operation, args []string, input []byte) (Result, error) {
	result, started, err := s.runner.run(ctx, args, input, max(op.output, 0), max(op.stderr, 0))
	op.output -= int64(len(result.Stdout))
	op.stderr -= int64(len(result.Stderr))

	if err == nil {
		return result, nil
	}

	if klass := classifyStderr(result.Stderr); klass != nil {
		err = errors.Join(klass, err)
	}

	effect := NotSent
	if started {
		effect = Unknown
	}

	return result, &CommandError{Command: "process", Result: cloneResult(result), Outcome: Outcome{Effect: effect, Steps: nil, Created: nil}, Timeout: contextSource(op.callerDone, err), Err: err}
}

func isNotFoundStderr(s string) bool {
	for _, prefix := range []string{"can't find pane:", "can't find window:", "can't find session:", "can't find client:", "no such buffer:", "no buffer "} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}

	return false
}

func isNoServerStderr(s string) bool {
	return (strings.HasPrefix(s, "error connecting to ") && strings.Contains(s, "(No such file or directory)")) || strings.HasPrefix(s, "no server running on ")
}

func classifyStderr(data []byte) error {
	// Only exact errno diagnostics distinguish a missing endpoint from permission,
	// malformed sockets, and vendor-specific failures. Diagnostics remain available.
	s := string(data)
	if strings.Contains(s, "Permission denied") || strings.Contains(s, "Operation not permitted") {
		return os.ErrPermission
	}

	if isNoServerStderr(s) {
		return ErrNoServer
	}

	if isNotFoundStderr(s) {
		return ErrNotFound
	}

	if strings.HasPrefix(s, "multiple sessions:") || strings.HasPrefix(s, "ambiguous ") {
		return ErrAmbiguousTarget
	}

	return nil
}

func (s *Server) execute(ctx context.Context, op *operation, p plan, g *guard, input []byte) (Result, error) {
	failed := func(err error) (Result, error) {
		r := failedResult()
		return r, &CommandError{Command: planName(p), Result: r, Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}

	if s.lifetime != nil {
		if err := s.lifetime.closedError(); err != nil {
			return failed(err)
		}
	}

	g, p = s.resolveGuard(p, g)

	n, err := s.checkInputLimit(op, p, input)
	if err != nil {
		return failed(err)
	}

	r, err := s.dispatchPlan(ctx, op, p, n, input)
	if g != nil {
		r, err = g.unwrap(r, err)
	}

	if err != nil {
		if ce, ok := errors.AsType[*CommandError](err); ok {
			ce.Command = planName(p)
		}

		return r, err
	}

	if err = ctx.Err(); err != nil {
		return r, &CommandError{Command: planName(p), Result: cloneResult(r), Outcome: Outcome{Effect: Confirmed, Steps: nil, Created: nil}, Timeout: contextSource(op.callerDone, err), Err: err}
	}

	return r, nil
}

func planName(p plan) string {
	if len(p.nodes) == 1 {
		return p.nodes[0].name
	}

	return "sequence"
}

func (s *Server) checkInputLimit(op *operation, p plan, input []byte) (int64, error) {
	n, err := p.size(op.input)
	if err != nil {
		return 0, err
	}

	total := n
	if s.conn != nil {
		if n > op.input-controlWireOverhead {
			return 0, ErrInputLimit
		}

		total += controlWireOverhead
	}

	if int64(len(input)) > op.input-total {
		return 0, ErrInputLimit
	}

	op.input -= total + int64(len(input))

	return total, nil
}

func (s *Server) resolveGuard(p plan, g *guard) (*guard, plan) {
	if s.bound != nil && g == nil && planName(p) != "show-buffer" {
		g = newGuard(*s.bound)
	}

	if g != nil {
		p = g.wrap(p)
	}

	return g, p
}

func (s *Server) dispatchPlan(ctx context.Context, op *operation, p plan, n int64, input []byte) (Result, error) {
	if s.conn != nil {
		if input != nil {
			return failedResult(), unsupportedTransport("stdin over control", Control, ErrTransportUnsupported)
		}

		r, err := s.conn.run(ctx, op, p, n)
		op.output -= int64(len(r.Stdout))
		op.stderr -= int64(len(r.Stderr))

		return r, err
	}

	args, err := p.argv()
	if err != nil {
		return failedResult(), err
	}

	args = append(s.baseArgs(p.allowStart), args...)

	return s.executeProcess(ctx, op, args, input)
}
