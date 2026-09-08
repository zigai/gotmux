package tmux

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"

	"example.com/tmux/internal/codec"
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
		n, e := strconv.ParseUint(fields[i+1], 10, 64)
		if e != nil {
			return frameID{}, ErrProtocol
		}
		*v = n
	}
	if id.flags > 1 {
		return frameID{}, ErrProtocol
	}
	return id, nil
}
func boundedLine(r *bufio.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, ErrOutputLimit
	}
	var out []byte
	for {
		part, e := r.ReadSlice('\n')
		if int64(len(part)) > max-int64(len(out)) {
			return nil, ErrOutputLimit
		}
		out = append(out, part...)
		if e == nil {
			return out, nil
		}
		if !errors.Is(e, bufio.ErrBufferFull) {
			return nil, e
		}
	}
}

// readControlUnit never scans for a delimiter inside a length-framed record.
// Arbitrary output-producing commands are not accepted on this transport.
func readControlUnit(r *bufio.Reader, max int64, publish func(Event)) (controlUnit, error) {
	line, e := boundedLine(r, max)
	if e != nil {
		return controlUnit{}, e
	}
	if !bytes.HasPrefix(line, []byte("%begin ")) {
		if bytes.HasPrefix(line, []byte("%end ")) || bytes.HasPrefix(line, []byte("%error ")) {
			return controlUnit{}, ErrProtocol
		}
		event, e := decodeEvent(line, max)
		return controlUnit{event: event}, e
	}
	id, e := frameHeader(line, "begin")
	if e != nil {
		return controlUnit{}, e
	}
	f := &controlFrame{id: id}
	used := int64(len(line))
	ordinary := false
	for {
		prefix, e := r.Peek(len(codec.RecordPrefix))
		if e != nil {
			return controlUnit{}, e
		}
		if string(prefix) == codec.RecordPrefix {
			wire, _, e := codec.ReadRecord(r, max-used)
			if e != nil {
				return controlUnit{}, errors.Join(ErrProtocol, e)
			}
			used += int64(len(wire))
			f.data = append(f.data, wire...)
			continue
		}
		line, e = boundedLine(r, max-used)
		if e != nil {
			return controlUnit{}, e
		}
		used += int64(len(line))
		ending := ""
		if bytes.HasPrefix(line, []byte("%end ")) {
			ending = "end"
		}
		if bytes.HasPrefix(line, []byte("%error ")) {
			ending = "error"
		}
		if ending != "" {
			end, e := frameHeader(line, ending)
			if e != nil || end != id {
				return controlUnit{}, ErrProtocol
			}
			f.failed = ending == "error"
			if ordinary && !f.failed {
				return controlUnit{}, ErrProtocol
			}
			return controlUnit{frame: f}, nil
		}
		if bytes.HasPrefix(line, []byte("%begin ")) {
			return controlUnit{}, ErrProtocol
		}
		if len(line) > 0 && line[0] == '%' {
			event, e := decodeEvent(line, max)
			if e != nil {
				return controlUnit{}, e
			}
			publish(event)
			continue
		}
		if !(bytes.HasPrefix(line, []byte("TGO-GUARD-1:")) || bytes.HasPrefix(line, []byte("TGO-DONE:")) || bytes.HasPrefix(line, []byte("TGO-READY:"))) {
			ordinary = true
		}
		f.data = append(f.data, line...)
	}
}

var _ io.Reader = (*bufio.Reader)(nil)
