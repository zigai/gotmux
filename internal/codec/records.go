package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Records use a versioned count plus netstrings. tmux's n: modifier is byte
// strlen, not display width. No byte is assumed absent from a format value.
const RecordPrefix = "TGO1:"

func RecordFormat(fields []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d:", RecordPrefix, len(fields))
	for _, f := range fields {
		fmt.Fprintf(&b, "#{n:%s}:#{%s},", f, f)
	}
	return b.String()
}
func ExpressionFormat(expr string) string {
	// Both expansions occur in one tmux format tree. Intended for deterministic
	// format expressions; job-producing formats are not a synchronization API.
	return RecordPrefix + "1:#{n:#{l:}" + expr + "}:" + expr + ","
}
func EncodeRecord(values []string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s%d:", RecordPrefix, len(values))
	for _, v := range values {
		fmt.Fprintf(&b, "%d:", len(v))
		b.WriteString(v)
		b.WriteByte(',')
	}
	b.WriteByte('\n')
	return b.Bytes()
}

var ErrRecord = errors.New("invalid length-prefixed record")

type Reader interface {
	io.Reader
	ReadByte() (byte, error)
}

func decimal(r Reader, max int64) (int64, int64, error) {
	var value, count int64
	var zero bool
	for {
		ch, e := r.ReadByte()
		if e != nil {
			return 0, count, e
		}
		count++
		if ch == ':' {
			if count == 1 {
				return 0, count, ErrRecord
			}
			return value, count, nil
		}
		if ch < '0' || ch > '9' || count > 19 || (zero && count > 1) {
			return 0, count, ErrRecord
		}
		if count == 1 {
			zero = ch == '0'
		}
		digit := int64(ch - '0')
		if value > (max-digit)/10 || max < digit {
			return 0, count, ErrRecord
		}
		value = value*10 + digit
	}
}

// ReadRecord consumes exactly one complete record including its terminating LF.
// The prefix has not yet been consumed. Encoded overhead and field count are
// bounded as well as content; a malicious length never drives an allocation.
func ReadRecord(r Reader, max int64) ([]byte, []string, error) {
	if max < int64(len(RecordPrefix)+3) {
		return nil, nil, ErrRecord
	}
	var wire bytes.Buffer
	prefix := make([]byte, len(RecordPrefix))
	if _, e := io.ReadFull(r, prefix); e != nil {
		return nil, nil, e
	}
	if string(prefix) != RecordPrefix {
		return nil, nil, ErrRecord
	}
	wire.Write(prefix)
	count, n, e := decimal(r, 1024)
	if e != nil {
		return nil, nil, e
	}
	if count > 1024 {
		return nil, nil, ErrRecord
	}
	wire.WriteString(strconv.FormatInt(count, 10))
	wire.WriteByte(':')
	used := int64(len(prefix)) + n
	fields := make([]string, 0, int(count))
	for i := int64(0); i < count; i++ {
		left := max - used
		if left <= 0 {
			return nil, nil, ErrRecord
		}
		size, n, e := decimal(r, left)
		if e != nil {
			return nil, nil, e
		}
		used += n
		if size > max-used-1 {
			return nil, nil, ErrRecord
		}
		b := make([]byte, int(size))
		if _, e = io.ReadFull(r, b); e != nil {
			return nil, nil, e
		}
		ch, e := r.ReadByte()
		if e != nil {
			return nil, nil, e
		}
		if ch != ',' {
			return nil, nil, ErrRecord
		}
		wire.WriteString(strconv.FormatInt(size, 10))
		wire.WriteByte(':')
		wire.Write(b)
		wire.WriteByte(',')
		used += size + 1
		fields = append(fields, string(b))
	}
	if used >= max {
		return nil, nil, ErrRecord
	}
	ch, e := r.ReadByte()
	if e != nil {
		return nil, nil, e
	}
	if ch != '\n' {
		return nil, nil, ErrRecord
	}
	wire.WriteByte(ch)
	return wire.Bytes(), fields, nil
}
func ParseRecords(data []byte, fieldCount int) ([][]string, error) {
	out := make([][]string, 0)
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		_, fields, e := ReadRecord(r, int64(r.Len()))
		if e != nil {
			return nil, e
		}
		if len(fields) != fieldCount {
			return nil, ErrRecord
		}
		out = append(out, fields)
	}
	return out, nil
}

// Octal decodes control output's exact \\ooo dialect; it never treats malformed
// escapes as literal text or normalizes CR/LF/non-UTF-8 bytes.
func Octal(data []byte, max int64) ([]byte, error) {
	if max < 0 || max > (1<<63-1)/4 || int64(len(data)) > max*4 {
		return nil, ErrRecord
	}
	out := make([]byte, 0, min(len(data), int(max)))
	for i := 0; i < len(data); i++ {
		b := data[i]
		if b == '\\' {
			if i+3 >= len(data) {
				return nil, ErrRecord
			}
			var x uint16
			for j := 1; j <= 3; j++ {
				c := data[i+j]
				if c < '0' || c > '7' {
					return nil, ErrRecord
				}
				x = x*8 + uint16(c-'0')
			}
			if x > 255 {
				return nil, ErrRecord
			}
			b = byte(x)
			i += 3
		}
		if int64(len(out)) >= max {
			return nil, ErrRecord
		}
		out = append(out, b)
	}
	return out, nil
}
