package tmux

import (
	"context"
	"time"

	"example.com/tmux/internal/codec"
	"example.com/tmux/internal/schema"
)

// BufferRef explicitly distinguishes a name from tmux's most-recent automatic
// buffer. It is an endpoint-relative selection, not an immutable object ID.
type BufferRef struct {
	name      string
	automatic bool
	valid     bool
}

func NamedBuffer(name string) (BufferRef, error) {
	if name == "" || !codec.ValidString(name) {
		return BufferRef{}, invalid("buffer name")
	}
	return BufferRef{name: name, valid: true}, nil
}
func AutomaticBuffer() BufferRef         { return BufferRef{automatic: true, valid: true} }
func (b BufferRef) Name() (string, bool) { return b.name, b.valid && !b.automatic }
func (b BufferRef) args() ([]string, error) {
	if !b.valid {
		return nil, ErrInvalidArgument
	}
	if b.automatic {
		return []string{}, nil
	}
	return []string{"-b", b.name}, nil
}

type BufferInfo struct {
	Name    string
	Size    int
	Created Value[time.Time]
	rawRecord
	server       *Server
	origin       ServerIdentity
	originalName string
}

// Ref refers to the fetched buffer name; names may be reused inside one daemon.
func (b BufferInfo) Ref() BufferRef {
	if b.server == nil || !b.origin.valid() {
		return BufferRef{}
	}
	v, _ := NamedBuffer(b.originalName)
	return v
}
func (s *Server) Buffers(ctx context.Context) ([]BufferInfo, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return nil, opError("Buffers", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return nil, opError("Buffers", e)
	}
	fields := schema.WithIdentity([]string{"buffer_name", "buffer_size", "buffer_created"})
	r, e := s.execute(op, recordsPlan(command("list-buffers", "-F", codec.RecordFormat(fields))), &guard{identity: info.Identity}, nil)
	if e != nil {
		return nil, opError("Buffers", e)
	}
	rows, e := parseRaw(r.Stdout, fields, "buffer")
	if e != nil {
		return nil, afterError("Buffers", e)
	}
	out := []BufferInfo{}
	for _, raw := range rows {
		d, id := s.decoder(raw, "buffer", &info.Identity)
		name := d.str("buffer_name")
		size := d.nonnegative("buffer_size")
		v := BufferInfo{Name: name, Size: size, server: s, origin: id, originalName: name, rawRecord: rawRecord{raw: raw}}
		if raw["buffer_created"] != "" {
			v.Created = PresentValue(d.timestamp("buffer_created"))
		} else {
			v.Created = UnsupportedValue[time.Time]()
		}
		if name == "" {
			d.err = ErrProtocol
		}
		if d.err != nil {
			return nil, afterError("Buffers", d.err)
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Server) bufferOperation(ctx context.Context, name string, b BufferRef, extra []string, input []byte, output bool) (Result, error) {
	args, e := b.args()
	if e != nil {
		return Result{ExitCode: -1}, opError(name, e)
	}
	args = append(args, extra...)
	op, e := s.begin(ctx)
	if e != nil {
		return Result{ExitCode: -1}, opError(name, e)
	}
	defer op.close()
	if int64(len(input)) > op.input {
		return Result{ExitCode: -1}, opError(name, ErrInputLimit)
	}
	info, e := s.probe(op)
	if e != nil {
		return Result{ExitCode: -1}, opError(name, e)
	}
	p := emptyPlan(command(name, args...))
	var g *guard
	if output {
		p.mode = replyRaw
	} else {
		g = &guard{identity: info.Identity}
	}
	r, e := s.execute(op, p, g, input)
	return r, opError(name, e)
}

// WriteBuffer sends binary bytes through bounded stdin. A zero-length slice
// is rejected: stock tmux treats empty writes as a no-op, not replacement. Use Connection.AuxiliaryServer explicitly in control
// mode; binary bytes are never interpolated into tmux command text.
func (s *Server) WriteBuffer(ctx context.Context, b BufferRef, data []byte) error {
	if len(data) == 0 {
		return opError("WriteBuffer", &UnsupportedError{Feature: "zero-length buffer replacement is not representable in stock tmux"})
	}
	_, e := s.bufferOperation(ctx, "load-buffer", b, []string{"--", "-"}, data, false)
	return e
}
func (s *Server) ReadBuffer(ctx context.Context, b BufferRef) ([]byte, error) {
	r, e := s.bufferOperation(ctx, "show-buffer", b, nil, nil, true)
	return r.Stdout, e
}
func (s *Server) DeleteBuffer(ctx context.Context, b BufferRef) error {
	_, e := s.bufferOperation(ctx, "delete-buffer", b, nil, nil, false)
	return e
}
func (s *Server) LoadBufferFile(ctx context.Context, b BufferRef, path string) error {
	if path == "" || path == "-" || !codec.ValidString(path) {
		return opError("LoadBufferFile", invalid("path"))
	}
	_, e := s.bufferOperation(ctx, "load-buffer", b, []string{"--", codec.LiteralFormat(path)}, nil, false)
	return e
}
func (s *Server) SaveBufferFile(ctx context.Context, b BufferRef, path string, appendFile bool) error {
	if path == "" || path == "-" || !codec.ValidString(path) {
		return opError("SaveBufferFile", invalid("path"))
	}
	args := []string{}
	if appendFile {
		args = append(args, "-a")
	}
	args = append(args, "--", codec.LiteralFormat(path))
	_, e := s.bufferOperation(ctx, "save-buffer", b, args, nil, false)
	return e
}

type PasteOptions struct {
	DeleteAfter bool
	Bracketed   bool
	Separator   *string
	RawNewlines bool
}

func (p Pane) PasteBuffer(ctx context.Context, b BufferRef, o PasteOptions) error {
	args, e := b.args()
	if e != nil {
		return opError("PasteBuffer", e)
	}
	if o.RawNewlines && o.Separator != nil {
		return opError("PasteBuffer", invalid("separator and raw newlines conflict"))
	}
	args = append(args, "-t", p.h.id)
	if o.DeleteAfter {
		args = append(args, "-d")
	}
	if o.Bracketed {
		args = append(args, "-p")
	}
	if o.RawNewlines {
		args = append(args, "-r")
	}
	if o.Separator != nil {
		if !codec.ValidString(*o.Separator) {
			return opError("PasteBuffer", invalid("separator"))
		}
		args = append(args, "-s", *o.Separator)
	}
	return p.h.act(ctx, "paste-buffer", args...)
}
