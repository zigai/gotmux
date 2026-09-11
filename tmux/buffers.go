package tmux

import (
	"context"
	"time"

	"github.com/zigai/gotmux/internal/schema"
	"github.com/zigai/gotmux/internal/wire"
)

// BufferRef identifies a tmux paste buffer by explicit name or automatic recent stack.
// Unlike [Session] handles, names are endpoint-relative and mutable.
type (
	BufferRef struct {
		name      string
		automatic bool
		valid     bool
	}

	// BufferInfo captures a point-in-time metadata snapshot of a paste buffer.
	BufferInfo struct {
		rawRecord

		// Name is the buffer name (e.g. "buffer0" or a custom name).
		Name string

		// Size is the length of the buffer content in bytes (#{buffer_size}).
		Size int

		// Created is the timestamp when the buffer was populated (#{buffer_created}).
		Created Value[time.Time]

		server       *Server
		origin       ServerIdentity
		originalName string
	}

	// PasteOptions configures how a buffer's content is pasted into a target pane.
	PasteOptions struct {
		// DeleteAfter deletes the buffer immediately after pasting (-d flag).
		DeleteAfter bool

		// Bracketed wraps pasted text in bracketed paste escape sequences (-p flag).
		Bracketed bool

		// Separator specifies an optional delimiter between lines (-s flag). Conflicts with RawNewlines.
		Separator *string

		// RawNewlines prevents replacing LF with CR when pasting (-r flag). Conflicts with Separator.
		RawNewlines bool
	}
)

// NamedBuffer creates a [BufferRef] targeting an explicitly named paste buffer.
func NamedBuffer(name string) (BufferRef, error) {
	if name == "" || !wire.ValidString(name) {
		return BufferRef{name: "", automatic: false, valid: false}, invalid("buffer name")
	}

	return BufferRef{name: name, automatic: false, valid: true}, nil
}

// AutomaticBuffer creates a [BufferRef] targeting the most recently created or modified
// paste buffer on the server.
func AutomaticBuffer() BufferRef { return BufferRef{name: "", automatic: true, valid: true} }

// Name returns the explicit buffer name and true, or an empty string and false if this is
// an automatic buffer reference.
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

// Ref returns a [BufferRef] targeting this buffer by its observed name.
func (b BufferInfo) Ref() BufferRef {
	if b.server == nil || !b.origin.valid() {
		return BufferRef{name: "", automatic: false, valid: false}
	}

	v, _ := NamedBuffer(b.originalName)

	return v
}

// Buffers queries and returns point-in-time metadata for all paste buffers currently
// retained by the tmux server.
func (s *Server) Buffers(ctx context.Context) ([]BufferInfo, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Buffers", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Buffers", err)
	}

	fields := schema.WithIdentity([]string{"buffer_name", "buffer_size", "buffer_created"})

	r, err := s.execute(opCtx, op, recordsPlan(command("list-buffers", "-F", wire.RecordFormat(fields))), newGuard(info.Identity), nil)
	if err != nil {
		return nil, opError("Buffers", err)
	}

	rows, err := parseRaw(r.Stdout, fields, "buffer")
	if err != nil {
		return nil, afterError("Buffers", err)
	}

	out := []BufferInfo{}

	for _, raw := range rows {
		d, id := s.decoder(raw, "buffer", &info.Identity)
		name := d.str("buffer_name")
		size := d.nonnegative("buffer_size")

		created := UnsupportedValue[time.Time]()
		if raw["buffer_created"] != "" {
			created = PresentValue(d.timestamp("buffer_created"))
		}

		//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
		v := BufferInfo{rawRecord: rawRecord{raw: raw}, Name: name, Size: size, Created: created, server: s, origin: id, originalName: name}

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
	args, err := b.args()
	if err != nil {
		return failedResult(), opError(name, err)
	}

	args = append(args, extra...)

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError(name, err)
	}
	defer op.close()

	if int64(len(input)) > op.input {
		return failedResult(), opError(name, ErrInputLimit)
	}

	info, err := s.probe(opCtx, op)
	if err != nil {
		return failedResult(), opError(name, err)
	}

	p := emptyPlan(command(name, args...))

	var g *guard

	if output {
		p.mode = replyRaw
	} else {
		g = newGuard(info.Identity)
	}

	r, err := s.execute(opCtx, op, p, g, input)

	return r, opError(name, err)
}

// WriteBuffer loads binary data into the target paste buffer via stdin.
//
// Why zero-length is rejected:
// Stock tmux treats empty stdin as a no-op rather than clearing or replacing the buffer.
// Returning success would leave stale content silently, so we reject zero-length data with [ErrUnsupported].
//
// Over control mode:
// Binary stdin cannot be framed safely inside control mode command text.
// Callers must use [Connection.AuxiliaryServer] explicitly for binary buffer writes.
func (s *Server) WriteBuffer(ctx context.Context, b BufferRef, data []byte) error {
	if len(data) == 0 {
		return opError("WriteBuffer", unsupported("zero-length buffer replacement is not representable in stock tmux"))
	}

	_, err := s.bufferOperation(ctx, "load-buffer", b, []string{"--", "-"}, data, false)

	return err
}

// ReadBuffer reads and returns the complete raw byte contents of the target buffer.
func (s *Server) ReadBuffer(ctx context.Context, b BufferRef) ([]byte, error) {
	r, err := s.bufferOperation(ctx, "show-buffer", b, nil, nil, true)
	return r.Stdout, err
}

// DeleteBuffer deletes the specified paste buffer from tmux's buffer stack.
func (s *Server) DeleteBuffer(ctx context.Context, b BufferRef) error {
	_, err := s.bufferOperation(ctx, "delete-buffer", b, nil, nil, false)
	return err
}

// LoadBufferFile loads the contents of a filesystem file into the target buffer.
func (s *Server) LoadBufferFile(ctx context.Context, b BufferRef, path string) error {
	if path == "" || path == "-" || !wire.ValidString(path) {
		return opError("LoadBufferFile", invalid("path"))
	}

	_, err := s.bufferOperation(ctx, "load-buffer", b, []string{"--", wire.LiteralFormat(path)}, nil, false)

	return err
}

// SaveBufferFile writes the contents of the target buffer to a filesystem file,
// optionally appending if appendFile is true.
func (s *Server) SaveBufferFile(ctx context.Context, b BufferRef, path string, appendFile bool) error {
	if path == "" || path == "-" || !wire.ValidString(path) {
		return opError("SaveBufferFile", invalid("path"))
	}

	args := []string{}
	if appendFile {
		args = append(args, "-a")
	}

	args = append(args, "--", wire.LiteralFormat(path))
	_, err := s.bufferOperation(ctx, "save-buffer", b, args, nil, false)

	return err
}

// PasteBuffer pastes the contents of the target buffer into this pane according to opts.
func (p Pane) PasteBuffer(ctx context.Context, b BufferRef, o PasteOptions) error {
	args, err := b.args()
	if err != nil {
		return opError("PasteBuffer", err)
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
		if !wire.ValidString(*o.Separator) {
			return opError("PasteBuffer", invalid("separator"))
		}

		args = append(args, "-s", *o.Separator)
	}

	return p.h.act(ctx, "paste-buffer", args...)
}
