package codec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// RecordPrefix identifies records using a versioned count plus netstrings. tmux's n: modifier is byte
	// strlen, not display width. No byte is assumed absent from a format value.
	RecordPrefix       = "TGO1:"
	wantWire     uint8 = 1 << iota
	wantFields

	minRecordOverhead = 3
	maxHeaderFields   = 1024
	octalBase         = 8
	maxByteValue      = 255
)

var ErrRecord = errors.New("invalid length-prefixed record")

type Reader interface {
	io.Reader
	ReadByte() (byte, error)
}

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

// ReadRecord consumes exactly one complete record including its terminating LF.
// The prefix has not yet been consumed. Encoded overhead and field count are
// bounded as well as content; a malicious length never drives an allocation.
func ReadRecord(r Reader, maxBytes int64) ([]byte, []string, error) {
	return scanRecord(r, maxBytes, wantWire|wantFields)
}

// ReadRecordWire scans and verifies one complete record, returning only its wire bytes.
func ReadRecordWire(r Reader, maxBytes int64) ([]byte, error) {
	wire, _, err := scanRecord(r, maxBytes, wantWire)
	return wire, err
}

// ReadRecordFields scans and verifies one complete record, returning only its decoded fields.
func ReadRecordFields(r Reader, maxBytes int64) ([]string, error) {
	_, fields, err := scanRecord(r, maxBytes, wantFields)
	return fields, err
}

func ParseRecords(data []byte, fieldCount int) ([][]string, error) {
	out := make([][]string, 0)

	r := bytes.NewReader(data)
	for r.Len() > 0 {
		fields, e := ReadRecordFields(r, int64(r.Len()))
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
func Octal(data []byte, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 || maxBytes > (1<<63-1)/4 || int64(len(data)) > maxBytes*4 {
		return nil, ErrRecord
	}

	out := make([]byte, 0, min(int64(len(data)), maxBytes))

	for i := 0; i < len(data); i++ {
		b := data[i]
		if b == '\\' {
			decoded, err := decodeOctalEscape(data, i)
			if err != nil {
				return nil, err
			}

			b = decoded
			i += 3
		}

		if int64(len(out)) >= maxBytes {
			return nil, ErrRecord
		}

		out = append(out, b)
	}

	return out, nil
}

func decodeOctalEscape(data []byte, i int) (byte, error) {
	if i+3 >= len(data) {
		return 0, ErrRecord
	}

	var x uint16

	for j := 1; j <= 3; j++ {
		c := data[i+j]
		if c < '0' || c > '7' {
			return 0, ErrRecord
		}

		x = x*octalBase + uint16(c-'0')
	}

	if x > maxByteValue {
		return 0, ErrRecord
	}

	return byte(x), nil
}

func scanRecordHeader(r Reader, maxBytes int64) (int64, int64, error) {
	if maxBytes < int64(len(RecordPrefix)+minRecordOverhead) {
		return 0, 0, ErrRecord
	}

	prefix := make([]byte, len(RecordPrefix))
	if _, err := io.ReadFull(r, prefix); err != nil {
		return 0, 0, err //nolint:wrapcheck // Reader error is propagated directly
	}

	if string(prefix) != RecordPrefix {
		return 0, 0, ErrRecord
	}

	count, n, err := decimal(r, maxHeaderFields)
	if err != nil {
		return 0, 0, err
	}

	return count, int64(len(prefix)) + n, nil
}

func readRecordTerminator(r Reader) error {
	ch, err := r.ReadByte()
	if err != nil {
		return err //nolint:wrapcheck // Reader error is propagated directly
	}

	if ch != '\n' {
		return ErrRecord
	}

	return nil
}

func scanRecord(r Reader, maxBytes int64, mode uint8) ([]byte, []string, error) {
	count, used, err := scanRecordHeader(r, maxBytes)
	if err != nil {
		return nil, nil, err
	}

	var wire bytes.Buffer
	if mode&wantWire != 0 {
		wire.WriteString(RecordPrefix)
		wire.WriteString(strconv.FormatInt(count, 10))
		wire.WriteByte(':')
	}

	var fields []string
	if mode&wantFields != 0 {
		fields = make([]string, 0, int(count))
	}

	for range count {
		b, n, e := scanField(r, maxBytes-used)
		if e != nil {
			return nil, nil, e
		}

		if mode&wantWire != 0 {
			wire.WriteString(strconv.FormatInt(int64(len(b)), 10))
			wire.WriteByte(':')
			wire.Write(b)
			wire.WriteByte(',')
		}

		used += n

		if mode&wantFields != 0 {
			fields = append(fields, string(b))
		}
	}

	if used >= maxBytes {
		return nil, nil, ErrRecord
	}

	if err := readRecordTerminator(r); err != nil {
		return nil, nil, err
	}

	if mode&wantWire != 0 {
		wire.WriteByte('\n')

		return wire.Bytes(), fields, nil
	}

	return nil, fields, nil
}

func scanField(r Reader, left int64) ([]byte, int64, error) {
	if left <= 0 {
		return nil, 0, ErrRecord
	}

	size, n, e := decimal(r, left)
	if e != nil {
		return nil, 0, e
	}

	used := n
	if size > left-used-1 {
		return nil, 0, ErrRecord
	}

	b := make([]byte, int(size))
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, 0, err //nolint:wrapcheck // Reader error is propagated directly
	}

	ch, err := r.ReadByte()
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // Reader error is propagated directly
	}

	if ch != ',' {
		return nil, 0, ErrRecord
	}

	return b, used + size + 1, nil
}

func isInvalidDecimalChar(ch byte, count int64, zero bool) bool {
	if ch < '0' || ch > '9' {
		return true
	}

	return count > 19 || (zero && count > 1)
}

func decimal(r Reader, maxBytes int64) (int64, int64, error) {
	var (
		value, count int64
		zero         bool
	)

	for {
		ch, err := r.ReadByte()
		if err != nil {
			return 0, count, err //nolint:wrapcheck // Reader error is propagated directly
		}

		count++
		if ch == ':' {
			if count == 1 {
				return 0, count, ErrRecord
			}

			return value, count, nil
		}

		if isInvalidDecimalChar(ch, count, zero) {
			return 0, count, ErrRecord
		}

		if count == 1 {
			zero = ch == '0'
		}

		digit := int64(ch - '0')
		if value > (maxBytes-digit)/10 || maxBytes < digit {
			return 0, count, ErrRecord
		}

		value = value*10 + digit //nolint:mnd // base 10 conversion
	}
}
