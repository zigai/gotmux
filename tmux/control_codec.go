package tmux

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/codec"
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
			return out, nil
		}

		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err //nolint:wrapcheck // bufio.Reader error is propagated directly
		}
	}
}

// readControlUnit never scans for a delimiter inside a length-framed record.
// Arbitrary output-producing commands are not accepted on this transport.
func readControlUnit(r *bufio.Reader, maxBytes int64, publish func(Event)) (controlUnit, error) {
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
	wire, err := codec.ReadRecordWire(r, left)
	if err != nil {
		return nil, errors.Join(ErrProtocol, err)
	}

	return wire, nil
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
		if err != nil {
			return false, err
		}

		publish(event)

		return false, nil
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
		prefix, peekErr := r.Peek(len(codec.RecordPrefix))
		if peekErr != nil {
			return nil, peekErr //nolint:wrapcheck // bufio.Reader error is propagated directly
		}

		if string(prefix) == codec.RecordPrefix {
			wire, wireErr := readRecordWireChunk(r, maxBytes-used)
			if wireErr != nil {
				return nil, wireErr
			}

			used += int64(len(wire))
			f.data = append(f.data, wire...)

			continue
		}

		line, lineErr := boundedLine(r, maxBytes-used)
		if lineErr != nil {
			return nil, lineErr
		}

		used += int64(len(line))

		done, lineProcessErr := processFrameLine(line, id, maxBytes, f, &ordinary, publish)
		if lineProcessErr != nil {
			return nil, lineProcessErr
		}

		if done {
			return f, nil
		}
	}
}
