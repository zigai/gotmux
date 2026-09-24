package tmux

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	dscPreambleLength = 7
	dscTrailerLength  = 2
	dscEscapeByte     = 0x1b
)

type frameID struct {
	time   uint64
	number uint64
	flags  uint64
}
type controlFrame struct {
	id     frameID
	data   []byte
	failed bool
}
type controlUnit struct {
	frame *controlFrame
	event Event
}
type controlStreamReader struct {
	r       io.Reader
	hasCR   bool
	seenDSC bool
	pending []byte
}

func frameHeader(line []byte, kind string) (frameID, error) {
	fields := strings.Fields(strings.TrimSuffix(string(line), "\n"))
	if len(fields) != 4 || fields[0] != "%"+kind {
		return frameID{}, ErrProtocol
	}

	var id frameID

	vals := []*uint64{&id.time, &id.number, &id.flags}
	for i, v := range vals {
		n, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return frameID{}, ErrProtocol
		}

		*v = n
	}

	if id.flags > 1 {
		return frameID{}, ErrProtocol
	}

	return id, nil
}

func isDSCTrailer(b []byte) bool {
	return bytes.Equal(bytes.TrimSpace(b), []byte("\x1b\\"))
}

func sanitizeLineEnding(b []byte) []byte {
	if len(b) >= 2 && b[len(b)-2] == '\r' {
		return append(b[:len(b)-2], '\n')
	}

	return b
}

func boundedLine(r *bufio.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrOutputLimit
	}

	var out []byte

	for {
		part, err := r.ReadSlice('\n')
		if int64(len(part)) > limit-int64(len(out)) {
			return nil, ErrOutputLimit
		}

		out = append(out, part...)
		if err == nil {
			out = sanitizeLineEnding(out)
			if isDSCTrailer(out) {
				return nil, io.EOF
			}

			return out, nil
		}

		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && isDSCTrailer(out) {
				return nil, io.EOF
			}

			return nil, err //nolint:wrapcheck // bufio.Reader error is propagated directly
		}
	}
}

// readControlUnit never scans for a delimiter inside a length-framed record.
// Arbitrary output-producing commands are not accepted on this transport.
func readControlUnit(r *bufio.Reader, maxBytes int64, publish func(Event)) (controlUnit, error) {
	if b, err := r.Peek(1); err == nil && b[0] == dscEscapeByte {
		if dsc, err := r.Peek(dscPreambleLength); err == nil && bytes.Equal(dsc, []byte("\x1bP1000p")) {
			_, _ = r.Discard(dscPreambleLength)
		} else if trailer, err := r.Peek(dscTrailerLength); err == nil && bytes.Equal(trailer, []byte("\x1b\\")) {
			_, _ = r.Discard(dscTrailerLength)
			return controlUnit{frame: nil, event: nil}, io.EOF
		}
	}

	line, err := boundedLine(r, maxBytes)
	if err != nil {
		return controlUnit{frame: nil, event: nil}, err
	}

	if !bytes.HasPrefix(line, []byte("%begin ")) {
		if bytes.HasPrefix(line, []byte("%end ")) || bytes.HasPrefix(line, []byte("%error ")) {
			return controlUnit{frame: nil, event: nil}, ErrProtocol
		}

		event, err := decodeEvent(line, maxBytes)

		return controlUnit{frame: nil, event: event}, err
	}

	f, err := readFrame(r, line, maxBytes, publish)
	if err != nil {
		return controlUnit{frame: nil, event: nil}, err
	}

	return controlUnit{frame: f, event: nil}, nil
}

func frameEnding(line []byte) string {
	if bytes.HasPrefix(line, []byte("%end ")) {
		return "end"
	}

	if bytes.HasPrefix(line, []byte("%error ")) {
		return "error"
	}

	return ""
}

func isAllowedMarker(line []byte) bool {
	return bytes.HasPrefix(line, []byte("TGO-GUARD-1:")) ||
		bytes.HasPrefix(line, []byte("TGO-DONE:")) ||
		bytes.HasPrefix(line, []byte("TGO-READY:"))
}

func readRecordWireChunk(r *bufio.Reader, left int64) ([]byte, error) {
	wireChunk, err := wire.ReadRecordWire(r, left)
	if err != nil {
		if errors.Is(err, wire.ErrRecord) {
			rest, readErr := boundedLine(r, left)
			if readErr != nil {
				return nil, errors.Join(ErrProtocol, err, readErr)
			}

			return append(wireChunk, rest...), nil
		}

		return nil, errors.Join(ErrProtocol, err)
	}

	return wireChunk, nil
}

func checkFrameEnd(line []byte, ending string, id frameID, ordinary bool) (bool, error) {
	end, err := frameHeader(line, ending)
	if err != nil || end != id {
		return false, ErrProtocol
	}

	failed := ending == "error"
	if ordinary && !failed {
		return false, ErrProtocol
	}

	return failed, nil
}

func processFrameLine(line []byte, id frameID, maxBytes int64, f *controlFrame, ordinary *bool, publish func(Event)) (bool, error) {
	if ending := frameEnding(line); ending != "" {
		failed, err := checkFrameEnd(line, ending, id, *ordinary)
		if err != nil {
			return false, err
		}

		f.failed = failed

		return true, nil
	}

	if bytes.HasPrefix(line, []byte("%begin ")) {
		return false, ErrProtocol
	}

	if len(line) > 0 && line[0] == '%' {
		event, err := decodeEvent(line, maxBytes)
		if err == nil {
			publish(event)

			return false, nil
		}
	}

	if !isAllowedMarker(line) {
		*ordinary = true
	}

	f.data = append(f.data, line...)

	return false, nil
}

func readFrame(r *bufio.Reader, beginLine []byte, maxBytes int64, publish func(Event)) (*controlFrame, error) {
	id, err := frameHeader(beginLine, "begin")
	if err != nil {
		return nil, err
	}

	f := &controlFrame{id: id, data: nil, failed: false}
	used := int64(len(beginLine))
	ordinary := false

	for {
		prefix, peekErr := r.Peek(len(wire.RecordPrefix))
		if peekErr != nil {
			return nil, peekErr //nolint:wrapcheck // bufio.Reader error is propagated directly
		}

		if string(prefix) == wire.RecordPrefix {
			n, wireErr := appendRecordChunk(r, maxBytes, used, f)
			if wireErr != nil {
				return nil, wireErr
			}

			used += n

			continue
		}

		line, lineErr := boundedLine(r, maxBytes)
		if lineErr != nil {
			return nil, lineErr
		}

		before := len(f.data)

		done, lineProcessErr := processFrameLine(line, id, maxBytes, f, &ordinary, publish)
		if lineProcessErr != nil {
			return nil, lineProcessErr
		}

		if done {
			return f, nil
		}

		used += int64(len(f.data) - before)
		if used > maxBytes {
			return nil, ErrOutputLimit
		}
	}
}

func appendRecordChunk(r *bufio.Reader, maxBytes, used int64, f *controlFrame) (int64, error) {
	wireChunk, wireErr := readRecordWireChunk(r, maxBytes-used)
	if wireErr != nil {
		return 0, wireErr
	}

	f.data = append(f.data, wireChunk...)

	return int64(len(wireChunk)), nil
}

func newControlStreamReader(r io.Reader) io.Reader {
	return &controlStreamReader{
		r:       r,
		hasCR:   false,
		seenDSC: false,
		pending: nil,
	}
}

func (c *controlStreamReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for {
		if len(c.pending) > 0 {
			n := copy(p, c.pending)
			c.pending = c.pending[n:]

			return n, nil
		}

		readSlice := p
		if c.hasCR && len(p) > 1 {
			readSlice = p[1:]
		}

		n, err := c.r.Read(readSlice)
		if n == 0 {
			if c.hasCR {
				c.hasCR = false
				p[0] = '\r'

				return 1, err //nolint:wrapcheck // io.Reader contract propagates raw error
			}

			return 0, err //nolint:wrapcheck // io.Reader contract propagates raw error
		}

		src := c.filterDSC(readSlice[:n])
		dst := c.transform(src, p)

		if dst > 0 || err != nil {
			return dst, err //nolint:wrapcheck // io.Reader contract propagates raw error
		}
	}
}

func (c *controlStreamReader) filterDSC(src []byte) []byte {
	if !c.seenDSC {
		c.seenDSC = true

		if bytes.HasPrefix(src, []byte("\x1bP1000p")) {
			return src[dscPreambleLength:]
		}
	}

	return src
}

func (c *controlStreamReader) emit(p []byte, dst *int, b byte) {
	if *dst < len(p) {
		p[*dst] = b
		*dst++
	} else {
		c.pending = append(c.pending, b)
	}
}

func (c *controlStreamReader) transform(src []byte, p []byte) int {
	dst := 0

	for _, b := range src {
		if c.hasCR {
			c.hasCR = false
			if b != '\n' {
				c.emit(p, &dst, '\r')
			}
		}

		if b == '\r' {
			c.hasCR = true
			continue
		}

		c.emit(p, &dst, b)
	}

	return dst
}
