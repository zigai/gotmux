package tmux

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os/exec"
	"slices"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	capturePanesLabel   = "Server.CapturePanes"
	captureMarkerPrefix = "TGO-CAPTURE-1:"
	captureMissing      = "missing\n"
	captureTitle        = "pane:"
	captureEnd          = "end\n"

	// tmux refuses a client command whose arguments exceed its 16 KiB message size.
	captureCommandBytes   = 12 << 10
	captureSeparatorBytes = len(" ; ")
)

// PaneCapture is one pane's result from [Server.CapturePanes].
type PaneCapture struct {
	// CaptureResult holds the captured bytes and the title. With [ErrOutputLimit] in Err, Output is truncated.
	CaptureResult

	// Pane is the handle the capture was requested for.
	Pane Pane

	// Err reports why the pane was not captured. It wraps [ErrNotFound] when the pane no longer exists.
	Err error
}

type captureBatch struct {
	// Pane contents cannot predict the marker, so they cannot forge a block boundary.
	marker   string
	maxBytes int64
	guard    *guard
	chunks   [][]wireNode
}

// CapturePanes captures the text and title of several panes of one daemon in as few tmux round trips as
// the command size limit allows, usually one. The result holds one entry per pane, in the given order.
// A pane that no longer exists, or whose capture tmux rejects, is reported in its entry's Err and leaves
// the other entries intact. Control mode cannot unambiguously frame captured terminal output; use a
// subprocess server.
func (s *Server) CapturePanes(
	ctx context.Context,
	panes []Pane,
	opts CaptureOptions,
) ([]PaneCapture, error) {
	batch, err := s.captureBatch(panes, opts)
	if err != nil {
		return nil, opError(capturePanesLabel, err)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError(capturePanesLabel, err)
	}
	defer op.close()

	out := make([]PaneCapture, 0, len(panes))

	for _, chunk := range batch.chunks {
		p := plan{nodes: chunk, mode: replyRaw, allowStart: false}
		r, runErr := s.execute(opCtx, op, p, batch.guard, nil)

		captures, decodeErr := batch.captures(
			panes[len(out):len(out)+len(chunk)],
			r,
			runErr,
			opCtx.Err(),
		)
		if decodeErr != nil {
			return nil, opError(capturePanesLabel, decodeErr)
		}

		out = append(out, captures...)
	}

	return out, nil
}

func (s *Server) captureBatch(panes []Pane, opts CaptureOptions) (captureBatch, error) {
	if s == nil || s.runner == nil {
		return captureBatch{}, ErrInvalidHandle
	}

	g, err := s.captureGuard(panes)
	if err != nil {
		return captureBatch{}, err
	}

	if opts.Buffer != "" {
		return captureBatch{}, invalid("capture buffer")
	}

	if opts.MaxBytes > s.config.Limits.OutputBytes {
		return captureBatch{}, invalid("MaxBytes may only tighten the configured limit")
	}

	batch := captureBatch{
		marker:   captureMarkerPrefix + rand.Text() + ":",
		maxBytes: opts.MaxBytes,
		guard:    g,
		chunks:   nil,
	}

	nodes, err := batch.nodes(panes, opts)
	if err != nil {
		return captureBatch{}, err
	}

	batch.chunks, err = captureChunks(g, nodes)

	return batch, err
}

func (s *Server) captureGuard(panes []Pane) (*guard, error) {
	var g *guard

	for _, p := range panes {
		if err := p.h.check(); err != nil {
			return nil, err
		}

		if p.h.server.endpoint != s.endpoint ||
			(p.h.server.lifetime != nil && s.lifetime != nil && p.h.server.lifetime != s.lifetime) {
			return nil, ErrInvalidHandle
		}

		paneGuard := p.h.guard()

		switch {
		case paneGuard == nil:
		case g == nil:
			g = paneGuard
		case !g.identity.sameDaemon(paneGuard.identity):
			return nil, ErrInvalidHandle
		}
	}

	if g == nil && s.bound != nil {
		g = newGuard(*s.bound)
	}

	return g, nil
}

func (b captureBatch) nodes(panes []Pane, opts CaptureOptions) ([]wireNode, error) {
	nodes := make([]wireNode, len(panes))

	for i, p := range panes {
		args, err := captureArgs(p.h.id, opts)
		if err != nil {
			return nil, err
		}

		title := command(
			"display-message",
			"-p",
			"-t",
			p.h.id,
			b.marker+captureTitle+wire.RecordFormat([]string{"pane_title"}),
		)

		// if-shell resolves a missing target without failing, and a failed capture skips only the
		// rest of its own branch, so every pane's outcome stays inside its own block.
		nodes[i] = conditionNode(
			eqFormat("pane_id", p.h.id),
			[]wireNode{
				leaf(title),
				leaf(command("capture-pane", args...)),
				markerNode(b.marker + captureEnd),
			},
			[]wireNode{markerNode(b.marker + captureMissing)},
			p.h.id,
		)
	}

	return nodes, nil
}

func captureChunks(g *guard, nodes []wireNode) ([][]wireNode, error) {
	base, err := commandBytes(g, nil)
	if err != nil {
		return nil, err
	}

	var chunks [][]wireNode

	start, size := 0, base

	for i, node := range nodes {
		alone, err := commandBytes(g, []wireNode{node})
		if err != nil {
			return nil, err
		}

		cost := alone - base + captureSeparatorBytes
		if base+cost > captureCommandBytes {
			return nil, ErrInputLimit
		}

		if size+cost > captureCommandBytes {
			chunks = append(chunks, nodes[start:i])
			start, size = i, base
		}

		size += cost
	}

	if start < len(nodes) {
		chunks = append(chunks, nodes[start:])
	}

	return chunks, nil
}

func commandBytes(g *guard, nodes []wireNode) (int, error) {
	p := plan{nodes: nodes, mode: replyRaw, allowStart: false}
	if g != nil {
		p = g.wrap(p)
	}

	args, err := p.argv()
	if err != nil {
		return 0, err
	}

	size := 0
	for _, arg := range args {
		size += len(arg) + 1
	}

	return size, nil
}

func (b captureBatch) captures(
	panes []Pane,
	r Result,
	err error,
	ctxErr error,
) ([]PaneCapture, error) {
	// tmux exits nonzero after writing its whole reply when it rejected a capture; any other
	// failure may have cut the reply short.
	rejecting := err != nil && r.ExitCode > 0 && ctxErr == nil && !errors.Is(err, ErrOutputLimit)
	if err != nil && !rejecting {
		return nil, err
	}

	out, rejected, ok := b.decode(r.Stdout, panes)
	if !ok || rejecting != (len(rejected) > 0) {
		if err != nil {
			return nil, err
		}

		return nil, afterError(capturePanesLabel, ErrProtocol)
	}

	rejections := captureRejections(r, err, len(rejected))
	for i, index := range rejected {
		out[index].Err = rejections[i]
	}

	for i := range out {
		if errors.Is(out[i].Err, ErrOutputLimit) {
			out[i].Err = afterError(capturePanesLabel, out[i].Err)
		}

		out[i].Err = opError(capturePanesLabel, out[i].Err)
	}

	return out, nil
}

func captureRejections(r Result, err error, count int) []error {
	lines := bytes.SplitAfter(r.Stderr, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	exit, _ := errors.AsType[*exec.ExitError](err)
	out := make([]error, count)

	for i := range out {
		// tmux reports one line per rejected capture, in pane order.
		stderr := r.Stderr
		if len(lines) == count {
			stderr = lines[i]
		}

		out[i] = &CommandError{
			Command: "capture-pane",
			Result:  Result{Stdout: nil, Stderr: bytes.Clone(stderr), ExitCode: r.ExitCode},
			Outcome: Outcome{Effect: EffectRejected, Steps: nil, Created: nil},
			Timeout: TimeoutSourceNone,
			Err:     errors.Join(classifyStderr(stderr), exit),
		}
	}

	return out
}

func (b captureBatch) decode(data []byte, panes []Pane) ([]PaneCapture, []int, bool) {
	out := make([]PaneCapture, len(panes))

	var rejected []int

	for i, pane := range panes {
		out[i] = PaneCapture{
			CaptureResult: CaptureResult{Output: nil, Title: ""},
			Pane:          pane,
			Err:           nil,
		}

		rest, ended, ok := b.decodeBlock(data, &out[i])
		if !ok {
			return nil, nil, false
		}

		if !ended {
			rejected = append(rejected, i)
		}

		data = rest
	}

	return out, rejected, len(data) == 0
}

func (b captureBatch) decodeBlock(data []byte, entry *PaneCapture) ([]byte, bool, bool) {
	marker := []byte(b.marker)

	block, found := bytes.CutPrefix(data, marker)
	if !found {
		return nil, false, false
	}

	if rest, missing := bytes.CutPrefix(block, []byte(captureMissing)); missing {
		entry.Err = ErrNotFound

		return rest, true, true
	}

	block, found = bytes.CutPrefix(block, []byte(captureTitle))
	if !found {
		return nil, false, false
	}

	reader := bytes.NewReader(block)

	fields, err := wire.ReadRecordFields(reader, int64(reader.Len()))
	if err != nil || len(fields) != 1 {
		return nil, false, false
	}

	entry.Title = fields[0]
	block = block[len(block)-reader.Len():]

	output, after, found := bytes.Cut(block, marker)
	if rest, ended := bytes.CutPrefix(after, []byte(captureEnd)); found && ended {
		entry.Output = output
		if b.maxBytes > 0 && int64(len(output)) > b.maxBytes {
			entry.Output, entry.Err = output[:b.maxBytes], ErrOutputLimit
		}

		// Entries share one reply buffer, so an append to one must not reach the next.
		entry.Output = slices.Clip(entry.Output)

		return rest, true, true
	}

	// A capture that tmux refused leaves a block without output or end marker.
	return block, false, len(output) == 0
}
